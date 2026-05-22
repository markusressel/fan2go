package controller

import (
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

type FanCurveAnalyzer struct {
	fanController *DefaultFanController
}

func NewFanCurveAnalyzer(
	fanController *DefaultFanController,
) *FanCurveAnalyzer {
	return &FanCurveAnalyzer{
		fanController: fanController,
	}
}

func (f *FanCurveAnalyzer) RunInitializationSequence() (rpmCurve map[int]float64, err error) {
	fan := f.fanController.fan

	if !fan.Supports(fans.FeatureRpmSensor) {
		ui.Info("Fan '%s' doesn't support RPM sensor, skipping fan curve measurement", fan.GetId())
		return nil, nil
	}
	ui.Info("Fan %s: Measuring RPM curve (two-phase adaptive sweep)...", fan.GetId())

	err = trySetManualPwm(fan)
	if err != nil {
		ui.Warning("Could not enable manual fan mode on %s, trying to continue anyway...", fan.GetId())
	}

	distinct := f.fanController.targetValuesWithDistinctPWMValue
	if len(distinct) == 0 {
		ui.Warning("Fan %s: no distinct PWM values found, skipping initialization", fan.GetId())
		return nil, nil
	}

	curveData := map[int]float64{}

	initStarted := time.Now()

	startBoundaryStarted := time.Now()
	startIdx, _, err := f.detectStartBoundary(fan, distinct, curveData)
	if err != nil {
		return nil, err
	}
	ui.Info("Fan %s: Boundary discovery (start) finished in %s", fan.GetId(), time.Since(startBoundaryStarted).Round(time.Millisecond))

	maxBoundaryStarted := time.Now()
	maxIdx, _, err := f.detectMaxBoundary(fan, distinct, startIdx, curveData)
	if err != nil {
		return nil, err
	}
	ui.Info("Fan %s: Boundary discovery (max) finished in %s", fan.GetId(), time.Since(maxBoundaryStarted).Round(time.Millisecond))

	interiorStarted := time.Now()
	curveData, err = f.sampleInteriorCoarse(fan, distinct, startIdx, maxIdx, curveData)
	if err != nil {
		return nil, err
	}
	ui.Info("Fan %s: Interior coarse sampling finished in %s", fan.GetId(), time.Since(interiorStarted).Round(time.Millisecond))

	curveData, err = f.rpmCurveMeasurementCleanup(curveData)
	if err != nil {
		return nil, err
	}

	ui.Info("Fan %s: RPM curve analysis finished in %s", fan.GetId(), time.Since(initStarted).Round(time.Millisecond))

	return curveData, nil
}

func (f *FanCurveAnalyzer) detectStartBoundary(
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
		pwm := distinct[mid]
		rpm, measureErr := f.measureAtPwm(fan, pwm, configuration.CurrentConfig.Analysis.SettleTimeout)
		if measureErr != nil {
			return 0, 0, measureErr
		}
		if rpm < 0 {
			// Progress search window even if this sample was skipped due to a transient mismatch.
			low = mid + 1
			continue
		}

		if fans.IsRpmLikelySpinning(rpm) {
			curveData[pwm] = rpm
			firstSpinning = mid
			high = mid - 1
		} else {
			curveData[pwm] = 0
			lastNotSpinning = mid
			low = mid + 1
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
		rpm, measureErr := f.measureAtPwm(fan, pwm, 0)
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
			measureRpm, measureErr := f.measureAtPwm(fan, pwm, 0)
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
		confirmedIdx, confirmedPwm, confirmErr := f.confirmMaxBoundary(fan, distinct, startIdx, roughIdx, curveData)
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
		rpm, measureErr := f.measureAtPwm(fan, pwm, configuration.CurrentConfig.Analysis.SettleTimeout)
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
		rpm, err := f.measureAtPwm(fan, pwm, settleTimeout)
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

func (f *FanCurveAnalyzer) rpmCurveMeasurementPhase1(
	fan fans.Fan,
	distinct []int,
	curveData map[int]float64,
) (map[int]float64, error) {
	coarseStep := configuration.CurrentConfig.Analysis.CoarseStep
	if coarseStep <= 0 {
		coarseStep = 1
	}
	// Build indices into distinct[], spaced coarseStep apart, always including first and last.
	coarseIndices := []int{distinct[0]}
	for i := coarseStep; i < len(distinct)-1; i += coarseStep {
		coarseIndices = append(coarseIndices, i)
	}
	lastIdx := len(distinct) - 1
	if coarseIndices[len(coarseIndices)-1] != lastIdx {
		coarseIndices = append(coarseIndices, lastIdx)
	}

	ui.Info("Fan %s: Phase 1: Coarse sweep at %d points (every %dth out of %d total distinct PWM values)",
		fan.GetId(), len(coarseIndices), coarseStep, len(distinct))
	for _, idx := range coarseIndices {
		pwm := distinct[idx]
		measuredRpm, err := f.measureAtPwm(fan, pwm, configuration.CurrentConfig.Analysis.SettleTimeout)
		if err != nil {
			ui.Error("Fan %s: Error measuring at PWM %d: %v", fan.GetId(), pwm, err)
			return curveData, err
		}
		if measuredRpm < 0 {
			continue // PWM mismatch detected, skip
		}
		ui.Debug("Fan %s: Phase 1: at PWM %d: %.1f RPM", fan.GetId(), pwm, measuredRpm)
		curveData[pwm] = measuredRpm
		fan.SetRpmAvg(measuredRpm)
	}

	return curveData, nil
}

func (f *FanCurveAnalyzer) rpmCurveMeasurementPhase2(
	fan fans.Fan,
	distinct []int,
	curveData map[int]float64,
) (map[int]float64, error) {
	coarsePwms := make([]int, 0, len(curveData))
	for pwm := range curveData {
		coarsePwms = append(coarsePwms, pwm)
	}
	sort.Ints(coarsePwms)

	alreadyMeasured := make(map[int]bool, len(curveData))
	for k := range curveData {
		alreadyMeasured[k] = true
	}
	toMeasure := map[int]bool{}

	// startPwm refinement: densely measure between the last coarse PWM with RPM=0
	// and the first coarse PWM with RPM>0.
	lastZeroPwm := -1
	firstNonZeroPwm := -1
	for _, pwm := range coarsePwms {
		if curveData[pwm] <= 0 {
			lastZeroPwm = pwm
		} else if firstNonZeroPwm < 0 {
			firstNonZeroPwm = pwm
		}
	}
	if firstNonZeroPwm >= 0 {
		lowBound := distinct[0]
		if lastZeroPwm >= 0 {
			lowBound = lastZeroPwm
		}
		for _, pwm := range distinct {
			if pwm > lowBound && pwm < firstNonZeroPwm && !alreadyMeasured[pwm] {
				toMeasure[pwm] = true
			}
		}
	} else {
		ui.Warning("Fan %s: coarse sweep found no RPM > 0, skipping startPwm refinement", fan.GetId())
	}

	// maxPwm refinement: densely measure between the coarse point just before and just after the peak.
	peakRpm := 0.0
	peakPwm := -1
	for _, pwm := range coarsePwms {
		if curveData[pwm] > peakRpm {
			peakRpm = curveData[pwm]
			peakPwm = pwm
		}
	}
	lastIdx := len(distinct) - 1
	if peakPwm >= 0 {
		maxRegionLow := distinct[0]
		maxRegionHigh := distinct[lastIdx]
		for i, pwm := range coarsePwms {
			if pwm == peakPwm {
				if i > 0 {
					maxRegionLow = coarsePwms[i-1]
				}
				if i < len(coarsePwms)-1 {
					maxRegionHigh = coarsePwms[i+1]
				}
				break
			}
		}
		for _, pwm := range distinct {
			if pwm > maxRegionLow && pwm < maxRegionHigh && !alreadyMeasured[pwm] {
				toMeasure[pwm] = true
			}
		}
	}

	// Collect, sort, and measure Phase 2 PWM values.
	phase2Pwms := make([]int, 0, len(toMeasure))
	for pwm := range toMeasure {
		phase2Pwms = append(phase2Pwms, pwm)
	}
	sort.Ints(phase2Pwms)

	if len(phase2Pwms) > 0 {
		ui.Info("Fan %s: Phase 2: Refining %d boundary points", fan.GetId(), len(phase2Pwms))
		for _, pwm := range phase2Pwms {
			measuredRpm, err := f.measureAtPwm(fan, pwm, configuration.CurrentConfig.Analysis.SettleTimeout)
			if err != nil {
				ui.Error("Fan %s: Error measuring at PWM %d: %v", fan.GetId(), pwm, err)
				return curveData, err
			}
			if measuredRpm < 0 {
				continue
			}
			ui.Debug("Fan %s: Phase 2: at PWM %d: %.1f RPM", fan.GetId(), pwm, measuredRpm)
			curveData[pwm] = measuredRpm
			fan.SetRpmAvg(measuredRpm)
		}
	}

	return curveData, nil
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
	// First Step: Interpolate stepwise (using util.InterpolateStep()) until the first value > 0 is reached, to ensure the curve data contains the critical "startPwm" point where the fan starts spinning.
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

	// Keep boundary detection (start/max PWM) based on robust raw statistics,
	// and only smooth the interior range to avoid boundary drift.
	startPwmRaw, maxPwmRaw := fans.ComputePwmBoundariesFromCurveData(interpolatedCurveData, fans.MaxPwmValue)
	if maxPwmRaw-startPwmRaw > 1 {
		interiorStart := startPwmRaw + 1
		interiorStop := maxPwmRaw - 1
		interpolatedCurveData = util.SmoothMapValuesKalman(interpolatedCurveData, interiorStart, interiorStop, util.DefaultKalmanConfig)
	}

	return interpolatedCurveData, nil
}

// measureAtPwm sets the fan to the given target PWM value, optionally waits for it to settle,
// takes SampleCount RPM samples spaced SampleDelay apart, and returns
// their median as a robust per-point estimate. Returns -1 (with nil error) if the reported PWM does not match the
// expected value after setting it (indicates the hardware ignored the request).
// If settleTimeout > 0, waitForFanToSettle is called with that timeout (used for large PWM steps).
// If settleTimeout == 0, a plain FanResponseDelay sleep is used instead (sufficient for small steps).
func (f *FanCurveAnalyzer) measureAtPwm(fan fans.Fan, pwm int, settleTimeout time.Duration) (float64, error) {
	actualPwm := f.fanController.applyPwmMapToTarget(pwm)
	matchedPwm := false
	for attempt := 0; attempt <= pwmMismatchRetries; attempt++ {
		err := f.fanController.setPwm(actualPwm)
		if err != nil {
			return 0, fmt.Errorf("unable to set PWM %d: %w", actualPwm, err)
		}
		expectedPwm := f.fanController.getReportedPwmAfterApplyingPwm(actualPwm)
		time.Sleep(f.fanController.getPwmSetDelay())

		currentPwm, err := f.fanController.getPwm()
		if err != nil {
			return 0, fmt.Errorf("fan %s: unable to read PWM: %w", fan.GetId(), err)
		}
		if currentPwm == expectedPwm {
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
		f.waitForFanToSettle(fan, settleTimeout)
	} else {
		// Small PWM step — a response-delay sleep is sufficient.
		time.Sleep(time.Duration(configuration.CurrentConfig.FanResponseDelay) * time.Second)
	}

	sampleCount := configuration.CurrentConfig.Analysis.SampleCount
	if sampleCount <= 0 {
		sampleCount = 1
	}
	sampleDelay := configuration.CurrentConfig.Analysis.SampleDelay
	samples := make([]float64, 0, sampleCount)
	for i := 0; i < sampleCount; i++ {
		if i > 0 {
			time.Sleep(sampleDelay)
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
func (f *FanCurveAnalyzer) waitForFanToSettle(fan fans.Fan, timeout time.Duration) {
	const requiredConsecutive = 3
	const settleSampleInterval = 500 * time.Millisecond
	const settleWindowSize = 8
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
		deadline = time.Now().Add(timeout)
	}

	consecutiveStable := 0
	for consecutiveStable < requiredConsecutive {
		if timeout > 0 && time.Now().After(deadline) {
			ui.Warning("Fan %s did not settle within %v, continuing anyway (%d/%d stable readings)", fan.GetId(), timeout, consecutiveStable, requiredConsecutive)
			return
		}
		time.Sleep(settleSampleInterval)

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
}

func evaluateAdaptiveSettling(window []float64, prevMean *float64, prevRange *float64, baseThreshold float64) (bool, float64, float64) {
	meanNow := util.Avg(window)
	minNow := util.MinValOrElse(window, meanNow)
	maxNow := util.MaxValOrElse(window, meanNow)
	rangeNow := maxNow - minNow

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
