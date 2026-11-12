package controller

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

const pwmMismatchRetries = 2

// defaultSettleSampleInterval is the time between two RPM readings while waiting for a fan to settle.
const defaultSettleSampleInterval = 500 * time.Millisecond

// sleepWithContext waits for d on the given clock. It returns ctx.Err() if ctx is done first,
// and also if ctx is already done when it is called (even for d <= 0).
func sleepWithContext(ctx context.Context, clock util.Clock, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-clock.After(d):
		return nil
	}
}

type FanCurveAnalyzer struct {
	fanController *DefaultFanController
	// settleSampleInterval is the time between two RPM readings in waitForFanToSettle
	settleSampleInterval time.Duration
}

func NewFanCurveAnalyzer(
	fanController *DefaultFanController,
) *FanCurveAnalyzer {
	return &FanCurveAnalyzer{
		fanController:        fanController,
		settleSampleInterval: defaultSettleSampleInterval,
	}
}

// clock returns the time source of the analyzed fan controller.
func (f *FanCurveAnalyzer) clock() util.Clock {
	return f.fanController.getClock()
}

func (f *FanCurveAnalyzer) RunInitializationSequence(ctx context.Context) (rpmCurve map[int]float64, err error) {
	fan := f.fanController.fan

	if !fan.Supports(fans.FeatureRpmSensor) {
		ui.Info("Fan '%s' doesn't support RPM sensor, skipping fan curve measurement", fan.GetId())
		return nil, nil
	}
	ui.Info("Fan %s: Measuring RPM curve (two-phase adaptive sweep)...", fan.GetId())

	// the measurement is meaningless (and would be persisted) if fan2go can't control the fan
	err = takeManualControl(fan)
	if err != nil {
		return nil, err
	}

	distinct := f.fanController.targetValuesWithDistinctPWMValue
	if len(distinct) == 0 {
		// saving no curve would make every start analyze the fan again
		return nil, fmt.Errorf("fan %s: no distinct PWM values found, unable to measure the RPM curve", fan.GetId())
	}

	curveData := map[int]float64{}

	clock := f.clock()
	initStarted := clock.Now()

	startBoundaryStarted := clock.Now()
	startIdx, _, err := f.detectStartBoundary(ctx, fan, distinct, curveData)
	if err != nil {
		return nil, err
	}
	ui.Info("Fan %s: Boundary discovery (start) finished in %s", fan.GetId(), clock.Now().Sub(startBoundaryStarted).Round(time.Millisecond))

	maxBoundaryStarted := clock.Now()
	maxIdx, _, err := f.detectMaxBoundary(ctx, fan, distinct, startIdx, curveData)
	if err != nil {
		return nil, err
	}
	ui.Info("Fan %s: Boundary discovery (max) finished in %s", fan.GetId(), clock.Now().Sub(maxBoundaryStarted).Round(time.Millisecond))

	interiorStarted := clock.Now()
	curveData, err = f.sampleInteriorCoarse(ctx, fan, distinct, startIdx, maxIdx, curveData)
	if err != nil {
		return nil, err
	}
	ui.Info("Fan %s: Interior coarse sampling finished in %s", fan.GetId(), clock.Now().Sub(interiorStarted).Round(time.Millisecond))

	curveData, err = f.rpmCurveMeasurementCleanup(curveData)
	if err != nil {
		return nil, err
	}

	ui.Info("Fan %s: RPM curve analysis finished in %s", fan.GetId(), clock.Now().Sub(initStarted).Round(time.Millisecond))

	return curveData, nil
}

func (f *FanCurveAnalyzer) detectStartBoundary(
	ctx context.Context,
	fan fans.Fan,
	distinct []int,
	curveData map[int]float64,
) (startIdx int, startPwm int, err error) {
	low := 0
	high := len(distinct) - 1
	firstSpinning := -1
	lastNotSpinning := -1

	ui.Info("Fan %s: Discovering start/min boundary...", fan.GetId())
	for low <= high {
		mid := (low + high) / 2
		// If the fan doesn't report the expected PWM at mid (the point is skipped), try its neighbours inside the
		// search window first: giving up on mid and searching only above it would make startPwm too high.
		probeIdx := -1
		rpm := -1.0
		for _, idx := range []int{mid, mid + 1, mid - 1} {
			if idx < low || idx > high {
				continue
			}
			measured, measureErr := f.measureAtPwm(ctx, fan, distinct[idx], configuration.CurrentConfig.Analysis.SettleTimeout)
			if measureErr != nil {
				return 0, 0, measureErr
			}
			if measured >= 0 {
				probeIdx = idx
				rpm = measured
				break
			}
			ui.Debug("Fan %s: skipped PWM %d in the start boundary search", fan.GetId(), distinct[idx])
		}
		if probeIdx < 0 {
			// Progress search window even if these samples were skipped due to transient mismatches.
			ui.Warning("Fan %s: the reported PWM doesn't match at and around PWM %d, skipping it in the start boundary search",
				fan.GetId(), distinct[mid])
			low = mid + 1
			continue
		}

		pwm := distinct[probeIdx]
		if fans.IsRpmLikelySpinning(rpm) {
			curveData[pwm] = rpm
			firstSpinning = probeIdx
			high = probeIdx - 1
		} else {
			curveData[pwm] = 0
			lastNotSpinning = probeIdx
			low = probeIdx + 1
		}
	}

	if firstSpinning < 0 {
		return 0, 0, fmt.Errorf("fan %s: unable to detect start boundary, no spinning PWM found", fan.GetId())
	}

	if lastNotSpinning >= 0 {
		curveData[distinct[lastNotSpinning]] = 0
	} else {
		// Ensure we have a zero anchor for interpolation and boundary stability.
		curveData[distinct[0]] = 0
	}

	startIdx = firstSpinning
	startPwm = distinct[firstSpinning]
	ui.Info("Fan %s: start/min boundary detected at PWM %d", fan.GetId(), startPwm)
	return startIdx, startPwm, nil
}

func (f *FanCurveAnalyzer) detectMaxBoundary(
	ctx context.Context,
	fan fans.Fan,
	distinct []int,
	startIdx int,
	curveData map[int]float64,
) (maxIdx int, maxPwm int, err error) {
	lastIdx := len(distinct) - 1
	step := configuration.CurrentConfig.Analysis.CoarseStep
	if step < 1 {
		step = 1
	}

	// Scan only the upper band to find peak-adjacent behavior quickly.
	topStartIdx := startIdx
	if lastIdx-topStartIdx > 64 {
		topStartIdx = lastIdx - 64
	}

	type point struct {
		idx int
		rpm float64
	}
	points := make([]point, 0, 1+((lastIdx-topStartIdx)/step)+1)
	fastScan := make(map[int]float64, 1+((lastIdx-topStartIdx)/step)+1)
	peakRpm := 0.0

	ui.Info("Fan %s: Discovering max boundary...", fan.GetId())
	for idx := lastIdx; idx >= topStartIdx; idx -= step {
		pwm := distinct[idx]
		rpm, measureErr := f.measureAtPwm(ctx, fan, pwm, 0)
		if measureErr != nil {
			return 0, 0, measureErr
		}
		if rpm < 0 {
			continue
		}
		fastScan[pwm] = rpm
		points = append(points, point{idx: idx, rpm: rpm})
		if rpm > peakRpm {
			peakRpm = rpm
		}
	}

	if len(points) == 0 {
		return 0, 0, fmt.Errorf("fan %s: unable to detect max boundary, no usable samples in top range", fan.GetId())
	}

	threshold := peakRpm * 0.95
	roughIdx := -1
	for idx := lastIdx; idx >= topStartIdx; idx-- {
		pwm := distinct[idx]
		rpm, exists := fastScan[pwm]
		if !exists {
			measureRpm, measureErr := f.measureAtPwm(ctx, fan, pwm, 0)
			if measureErr != nil {
				return 0, 0, measureErr
			}
			if measureRpm < 0 {
				continue
			}
			rpm = measureRpm
			fastScan[pwm] = rpm
		}

		if rpm >= threshold {
			roughIdx = idx
			break
		}
	}

	if roughIdx >= 0 {
		confirmedIdx, confirmedPwm, confirmErr := f.confirmMaxBoundary(ctx, fan, distinct, startIdx, roughIdx, curveData)
		if confirmErr != nil {
			return 0, 0, confirmErr
		}
		return confirmedIdx, confirmedPwm, nil
	}

	// Fallback in case the threshold is unusually strict due to outliers.
	fallbackIdx := points[len(points)-1].idx
	fallbackPwm := distinct[fallbackIdx]
	ui.Warning("Fan %s: no qualifying max boundary found in top range, using fallback PWM %d", fan.GetId(), fallbackPwm)
	return fallbackIdx, fallbackPwm, nil
}

func (f *FanCurveAnalyzer) confirmMaxBoundary(
	ctx context.Context,
	fan fans.Fan,
	distinct []int,
	startIdx int,
	roughIdx int,
	curveData map[int]float64,
) (maxIdx int, maxPwm int, err error) {
	lastIdx := len(distinct) - 1
	confirmIdxSet := map[int]struct{}{}

	for idx := max(startIdx, lastIdx-3); idx <= lastIdx; idx++ {
		confirmIdxSet[idx] = struct{}{}
	}
	for _, idx := range []int{roughIdx - 1, roughIdx, roughIdx + 1} {
		if idx >= startIdx && idx <= lastIdx {
			confirmIdxSet[idx] = struct{}{}
		}
	}

	confirmIndices := make([]int, 0, len(confirmIdxSet))
	for idx := range confirmIdxSet {
		confirmIndices = append(confirmIndices, idx)
	}
	sort.Ints(confirmIndices)

	peakConfirmed := 0.0
	for _, idx := range confirmIndices {
		pwm := distinct[idx]
		rpm, measureErr := f.measureAtPwm(ctx, fan, pwm, configuration.CurrentConfig.Analysis.SettleTimeout)
		if measureErr != nil {
			return 0, 0, measureErr
		}
		if rpm < 0 {
			continue
		}
		curveData[pwm] = rpm
		if rpm > peakConfirmed {
			peakConfirmed = rpm
		}
	}

	if peakConfirmed <= 0 {
		fallbackPwm := distinct[roughIdx]
		ui.Warning("Fan %s: max boundary confirmation failed, using rough candidate PWM %d", fan.GetId(), fallbackPwm)
		return roughIdx, fallbackPwm, nil
	}

	threshold := peakConfirmed * 0.95
	for idx := lastIdx; idx >= startIdx; idx-- {
		pwm := distinct[idx]
		rpm, exists := curveData[pwm]
		if !exists {
			continue
		}
		if rpm >= threshold {
			ui.Info("Fan %s: max boundary confirmed at PWM %d (threshold %.1f RPM)", fan.GetId(), pwm, threshold)
			return idx, pwm, nil
		}
	}

	fallbackPwm := distinct[roughIdx]
	ui.Warning("Fan %s: no confirmed max boundary above threshold, using rough candidate PWM %d", fan.GetId(), fallbackPwm)
	return roughIdx, fallbackPwm, nil
}

func (f *FanCurveAnalyzer) sampleInteriorCoarse(
	ctx context.Context,
	fan fans.Fan,
	distinct []int,
	startIdx int,
	maxIdx int,
	curveData map[int]float64,
) (map[int]float64, error) {
	if maxIdx <= startIdx+1 {
		return curveData, nil
	}

	step := configuration.CurrentConfig.Analysis.CoarseStep * 2
	if step < 8 {
		step = 8
	}

	count := 0
	lastMeasuredPwm := distinct[startIdx]
	for idx := startIdx + step; idx < maxIdx; idx += step {
		pwm := distinct[idx]
		if _, exists := curveData[pwm]; exists {
			lastMeasuredPwm = pwm
			continue
		}
		settleTimeout := settleTimeoutForPwmJump(lastMeasuredPwm, pwm, configuration.CurrentConfig.Analysis.SettleTimeout)
		rpm, err := f.measureAtPwm(ctx, fan, pwm, settleTimeout)
		if err != nil {
			return curveData, err
		}
		if rpm < 0 {
			continue
		}
		curveData[pwm] = rpm
		lastMeasuredPwm = pwm
		count++
	}

	ui.Info("Fan %s: Interior coarse sampling captured %d points", fan.GetId(), count)
	return curveData, nil
}

func settleTimeoutForPwmJump(previousPwm int, targetPwm int, fullSettleTimeout time.Duration) time.Duration {
	if util.Abs(targetPwm-previousPwm) >= 8 {
		return fullSettleTimeout
	}
	return 0
}

func (f *FanCurveAnalyzer) rpmCurveMeasurementCleanup(curveData map[int]float64) (interpolatedCurveData map[int]float64, err error) {
	if len(curveData) == 0 {
		return nil, fmt.Errorf("cannot clean up empty fan curve data")
	}

	firstNonZeroPwm := -1
	sortedPwmValues := util.SortedKeys(curveData)
	for _, pwm := range sortedPwmValues {
		if fans.IsRpmLikelySpinning(curveData[pwm]) {
			firstNonZeroPwm = pwm
			break
		}
	}
	if firstNonZeroPwm < 0 {
		return nil, fmt.Errorf("curve data contains no spinning sample above noise threshold")
	}

	for _, pwm := range sortedPwmValues {
		if pwm < firstNonZeroPwm || !fans.IsRpmLikelySpinning(curveData[pwm]) {
			curveData[pwm] = 0
		}
	}

	if _, exists := curveData[fans.MinPwmValue]; !exists {
		curveData[fans.MinPwmValue] = 0
	}
	if _, exists := curveData[fans.MaxPwmValue]; !exists {
		curveData[fans.MaxPwmValue] = curveData[sortedPwmValues[len(sortedPwmValues)-1]]
	}

	lastPwm := fans.MaxPwmValue

	// Interpolation Phase:
	// First Step: Interpolate stepwise (using util.InterpolateStep()) until the first sample above the spin/noise threshold is reached, to ensure the curve data contains the critical "startPwm" point where the fan is first considered to be spinning.
	// Second Step: Interpolate linearly (using util.InterpolateLinearly()) between all measured points to fill in any remaining gaps, ensuring the curve data contains the full range of PWM values as keys.
	interpolatedCurveData, err = util.InterpolateStep(&curveData, 0, firstNonZeroPwm)
	if err != nil {
		return nil, fmt.Errorf("error interpolating curve data: %v", err)
	}
	interpolatedCurveData, err = util.InterpolateLinearly(&interpolatedCurveData, firstNonZeroPwm, lastPwm)
	if err != nil {
		return nil, fmt.Errorf("error during linear interpolation of fan curve data: %w", err)
	}
	interpolatedCurveData = util.EnsureMonotonicallyIncreasing(interpolatedCurveData, firstNonZeroPwm, lastPwm)

	// Smooth only the range between the start PWM and PWM 255 (both exclusive): the start PWM keeps its measured
	// value, so the derived startPwm doesn't drift, and PWM 255 keeps the peak RPM.
	// The range doesn't depend on the derived maxPwm, which is computed from the smoothed curve.
	startPwmRaw, _ := fans.ComputePwmBoundariesFromCurveData(interpolatedCurveData, fans.MaxPwmValue)
	if fans.MaxPwmValue-startPwmRaw > 1 {
		interiorStart := startPwmRaw + 1
		interiorStop := fans.MaxPwmValue - 1
		interpolatedCurveData = util.SmoothMapValuesKalman(interpolatedCurveData, interiorStart, interiorStop, util.DefaultKalmanConfig)
	}

	return interpolatedCurveData, nil
}

// measureAtPwm sets the fan to the given target PWM value, optionally waits for it to settle,
// takes SampleCount RPM samples spaced SampleDelay apart, and returns
// their median as a robust per-point estimate. After each write it polls the reported PWM until it matches the
// expected value, for up to FanResponseDelay, and records the time it took as an observed readback delay.
// Returns -1 (with nil error) if the reported PWM does not match the expected value after
// pwmMismatchRetries+1 writes (indicates the hardware ignored the request).
// If settleTimeout > 0, waitForFanToSettle is called with that timeout (used for large PWM steps).
// If settleTimeout == 0, a plain FanResponseDelay sleep is used instead (sufficient for small steps).
func (f *FanCurveAnalyzer) measureAtPwm(ctx context.Context, fan fans.Fan, pwm int, settleTimeout time.Duration) (float64, error) {
	// pwm is a target value (pre-pwmMap). setTargetPwm applies the pwmMap itself, so the mapped
	// raw value is only used to compute the expected readback and for log messages.
	actualPwm := f.fanController.applyPwmMapToTarget(pwm)
	expectedPwm := f.fanController.getReportedPwmAfterApplyingPwm(actualPwm)
	matchedPwm := false
	for attempt := 0; attempt <= pwmMismatchRetries; attempt++ {
		writesBefore := f.fanController.pwmWriteCount
		err := f.fanController.setTargetPwm(pwm)
		if err != nil {
			return 0, fmt.Errorf("unable to set PWM %d (target %d): %w", actualPwm, pwm, err)
		}
		wrote := f.fanController.pwmWriteCount != writesBefore

		// The reported PWM may lag behind the requested value on some hardware: wait up to FanResponseDelay for it
		currentPwm, matched, delay, err := f.fanController.waitForReadback(ctx,
			func(reported int) bool { return reported == expectedPwm },
			configuration.CurrentConfig.FanResponseDelay.Duration())
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return 0, ctxErr
			}
			return 0, fmt.Errorf("fan %s: unable to read PWM: %w", fan.GetId(), err)
		}
		if matched {
			if wrote && fan.Supports(fans.FeaturePwmSensor) {
				f.fanController.observeReadbackDelay(delay)
			}
			matchedPwm = true
			break
		}

		ui.Debug("Fan %s: PWM mismatch at target %d: expected %d, got %d (attempt %d/%d)",
			fan.GetId(), pwm, expectedPwm, currentPwm, attempt+1, pwmMismatchRetries+1)
	}
	if !matchedPwm {
		ui.Debug("Fan %s: skipping target %d after repeated PWM mismatches", fan.GetId(), pwm)
		return -1, nil
	}

	if settleTimeout > 0 {
		if err := f.waitForFanToSettle(ctx, fan, settleTimeout); err != nil {
			return 0, err
		}
	} else {
		// Small PWM step — a response-delay sleep is sufficient.
		if err := sleepWithContext(ctx, f.clock(), configuration.CurrentConfig.FanResponseDelay.Duration()); err != nil {
			return 0, err
		}
	}

	sampleCount := configuration.CurrentConfig.Analysis.SampleCount
	if sampleCount <= 0 {
		sampleCount = 1
	}
	sampleDelay := configuration.CurrentConfig.Analysis.SampleDelay
	samples := make([]float64, 0, sampleCount)
	for i := 0; i < sampleCount; i++ {
		if i > 0 {
			if err := sleepWithContext(ctx, f.clock(), sampleDelay); err != nil {
				return 0, err
			}
		}
		r, err := fan.GetRpm()
		if err != nil {
			ui.Warning("Unable to read RPM of fan %s: %v", fan.GetId(), err)
			continue
		}
		samples = append(samples, float64(r))
	}
	if len(samples) == 0 {
		return 0, fmt.Errorf("no RPM samples collected at PWM %d", pwm)
	}
	return util.MedianFloat64(samples), nil
}

// waitForFanToSettle waits until the fan's RPM readings are stable (requiredConsecutive consecutive
// readings with diff <= MaxRpmDiffForSettledFan). If timeout > 0 and the deadline is exceeded, a
// warning is logged and the function returns early rather than blocking forever.
func (f *FanCurveAnalyzer) waitForFanToSettle(ctx context.Context, fan fans.Fan, timeout time.Duration) error {
	const requiredConsecutive = 3
	const settleWindowSize = 8
	clock := f.clock()
	settleSampleInterval := f.settleSampleInterval
	if settleSampleInterval <= 0 {
		settleSampleInterval = defaultSettleSampleInterval
	}
	const minSamplesForDecision = 6
	diffThreshold := configuration.CurrentConfig.MaxRpmDiffForSettledFan

	firstRpm := 0.0
	if r, err := fan.GetRpm(); err == nil {
		firstRpm = float64(r)
	}
	filter := util.NewKalmanFilter(util.DefaultKalmanConfig, firstRpm)
	window := make([]float64, 0, settleWindowSize)
	var prevMean *float64
	var prevRange *float64

	var deadline time.Time
	if timeout > 0 {
		deadline = clock.Now().Add(timeout)
	}

	consecutiveStable := 0
	for consecutiveStable < requiredConsecutive {
		if timeout > 0 && clock.Now().After(deadline) {
			ui.Warning("Fan %s did not settle within %v, continuing anyway (%d/%d stable readings)", fan.GetId(), timeout, consecutiveStable, requiredConsecutive)
			return nil
		}
		if err := sleepWithContext(ctx, clock, settleSampleInterval); err != nil {
			return err
		}

		currentRpm, err := fan.GetRpm()
		if err != nil {
			ui.Warning("Fan %s: Cannot read RPM value: %v", fan.GetId(), err)
			continue
		}

		filtered := filter.Update(float64(currentRpm))
		window = append(window, filtered)
		if len(window) > settleWindowSize {
			window = window[1:]
		}

		if len(window) < minSamplesForDecision {
			ui.Debug("Fan %s: Waiting for fan to settle (%d/%d stable readings, collecting baseline %d/%d)",
				fan.GetId(), consecutiveStable, requiredConsecutive, len(window), minSamplesForDecision)
			continue
		}

		stable, meanNow, rangeNow := evaluateAdaptiveSettling(window, prevMean, prevRange, diffThreshold)
		if stable {
			consecutiveStable++
		} else {
			consecutiveStable = 0
		}
		prevMean = &meanNow
		prevRange = &rangeNow

		ui.Debug("Fan %s: Waiting for fan to settle (%d/%d stable readings, mean=%.1f, noiseRange=%.1f)",
			fan.GetId(), consecutiveStable, requiredConsecutive, meanNow, rangeNow)
	}
	ui.Debug("Fan %s has settled (%d consecutive stable readings)", fan.GetId(), requiredConsecutive)
	return nil
}

// evaluateAdaptiveSettling judges a window of filtered RPM readings. The noise is the range of the readings around
// their linear trend (see detrendedRange): the plain range of a fan that still ramps up or down contains the ramp,
// which would make any steady ramp look like noise, and so like a settled fan.
func evaluateAdaptiveSettling(window []float64, prevMean *float64, prevRange *float64, baseThreshold float64) (bool, float64, float64) {
	meanNow := util.Avg(window)
	rangeNow := detrendedRange(window)

	// A minimum floor avoids treating near-zero quantization as "perfectly stable".
	effectiveNoise := math.Max(2.0, rangeNow)
	trendPerSample := math.Abs(window[len(window)-1]-window[0]) / float64(len(window)-1)

	if prevMean == nil || prevRange == nil {
		trendStable := trendPerSample <= math.Max(1.0, effectiveNoise*0.35)
		return trendStable, meanNow, rangeNow
	}

	drift := math.Abs(meanNow - *prevMean)
	rangeDelta := math.Abs(rangeNow - *prevRange)

	meanStable := drift <= math.Max(baseThreshold, effectiveNoise)
	noiseStable := rangeDelta <= math.Max(2.0, *prevRange*0.35)
	trendStable := trendPerSample <= math.Max(1.0, effectiveNoise*0.35)

	return meanStable && noiseStable && trendStable, meanNow, rangeNow
}

// detrendedRange returns max - min of the residuals of values around their least squares line
// (values are equally spaced). 0 for fewer than two values.
func detrendedRange(values []float64) float64 {
	n := len(values)
	if n < 2 {
		return 0
	}
	meanX := float64(n-1) / 2
	meanY := util.Avg(values)
	var covXY, varX float64
	for i, y := range values {
		dx := float64(i) - meanX
		covXY += dx * (y - meanY)
		varX += dx * dx
	}
	slope := covXY / varX
	minResidual, maxResidual := math.Inf(1), math.Inf(-1)
	for i, y := range values {
		residual := y - (meanY + slope*(float64(i)-meanX))
		minResidual = math.Min(minResidual, residual)
		maxResidual = math.Max(maxResidual, residual)
	}
	return maxResidual - minResidual
}
