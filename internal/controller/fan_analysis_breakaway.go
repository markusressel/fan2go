package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

const (
	// breakawayPollInterval is the time between two RPM readings during the breakaway measurement
	breakawayPollInterval = 100 * time.Millisecond
	// minBreakawayTryWindow is the shortest time a fan gets to start at a PWM value (after it reported the value).
	// It covers the restart cycle of the locked-rotor protection of BLDC fans (drive ~0.5s, pause 2-5s).
	minBreakawayTryWindow = 6 * time.Second
	// breakawayTryWindowTachIntervals: a try lasts at least this many tach intervals
	breakawayTryWindowTachIntervals = 3
	// breakawayStandstillReadings is the minimum number of readings below the spin threshold, spanning at least
	// minBreakawayStandstillSpan and two tach intervals, after which a fan counts as stopped
	breakawayStandstillReadings = 3
	minBreakawayStandstillSpan  = time.Second
	// the pause after a fan counts as stopped is half its coast-down time (from the write until the RPM was below the
	// spin threshold), within these bounds: a large fan coasts below its tach minimum for longer, and would start more
	// easily while it still turns
	minBreakawayStandstillPause = 2 * time.Second
	maxBreakawayStandstillPause = 10 * time.Second
	// defaultBreakawayStandstillTimeout is the longest wait for a fan to stop, if analysis.settleTimeout is 0
	defaultBreakawayStandstillTimeout = 30 * time.Second
	// breakawayCoarseSteps is the number of steps the coarse search of the breakaway measurement divides the
	// distinct PWM values into. The refinement stops at a quarter of a step.
	breakawayCoarseSteps = 16
	// breakawayStartReadings is the number of readings in a row that must show the fan spinning to count a try as
	// started (a single reading can be wrong)
	breakawayStartReadings = 2
	// minTachIntervalSamples is the number of intervals between RPM changes needed to measure the tach interval
	minTachIntervalSamples = 3
)

// breakawayResult is the result of the breakaway measurement, nil fields weren't measured.
type breakawayResult struct {
	// Pwm is the lowest PWM value (pre-pwmMap) that started the fan from standstill
	Pwm *int
	// TachInterval is the median time between two changes of the reported RPM value
	TachInterval *time.Duration
	// SpinUp is the time from the readback of Pwm to the first spinning RPM reading, from standstill
	SpinUp *time.Duration
	// CoastDown is the longest time the fan took from a stopping write until it counted as stopped
	CoastDown *time.Duration
}

// breakawayMeasurement keeps the observations of a breakaway measurement.
type breakawayMeasurement struct {
	analyzer *FanCurveAnalyzer
	fan      fans.Fan
	// times at which the reported RPM value changed
	changeTimes []time.Time
	lastRpm     int
	haveLastRpm bool
	// coastDown is the longest coast-down seen (0: none)
	coastDown time.Duration
}

// measureBreakawayPwm measures the lowest PWM value (pre-pwmMap) that starts the fan from standstill (the breakaway
// PWM), which can be much higher than the start boundary (the lowest value that keeps a spinning fan turning), and on
// the way the tach update interval and the spin-up delay of the fan. startIdx is the index of the start boundary in
// distinct.
//
// It stops the fan, then tries the start boundary and every breakawayCoarseSteps-th distinct value above it without
// stopping the fan in between, until one starts the fan. Then it bisects between the last value that didn't start
// the fan and the first that did, from standstill each time, down to a quarter of a coarse step, and confirms the
// result from standstill (moving up if that fails). Each try starts once the fan reports the written value, and lasts
// max(minBreakawayTryWindow, 3 tach intervals). The result has no Pwm if the fan doesn't stop (it spins at the lowest
// value, or doesn't stop within the settle timeout) or doesn't start at all.
func (f *FanCurveAnalyzer) measureBreakawayPwm(ctx context.Context, fan fans.Fan, distinct []int, startIdx int) (result breakawayResult, err error) {
	m := &breakawayMeasurement{analyzer: f, fan: fan}
	if startIdx <= 0 || startIdx >= len(distinct) {
		ui.Info("Fan %s: the fan spins at its lowest PWM value, skipping the breakaway measurement", fan.GetId())
		return result, nil
	}
	ui.Info("Fan %s: Measuring the breakaway PWM value (the lowest PWM value that starts the fan from standstill)...", fan.GetId())

	lastIdx := len(distinct) - 1
	step := max(1, ceilDiv(len(distinct), breakawayCoarseSteps))
	tolerance := max(1, step/4)
	stopTarget := distinct[0]

	defer func() {
		result.TachInterval = m.tachInterval()
		if m.coastDown > 0 {
			coastDown := m.coastDown
			result.CoastDown = &coastDown
		}
	}()

	stopped, err := m.stopAndWaitForStandstill(ctx, stopTarget)
	if err != nil {
		return result, err
	}
	if !stopped {
		ui.Warning("Fan %s: the fan didn't stop at PWM %d, skipping the breakaway measurement", fan.GetId(), stopTarget)
		return result, nil
	}

	// the start boundary is the lowest value that keeps the fan spinning: the value below it can't start it
	lastFailIdx := startIdx - 1
	firstStartIdx := -1
	// fromStandstill[idx]: idx started the fan from standstill, with this spin-up delay
	fromStandstill := map[int]time.Duration{}
	for idx, first := startIdx, true; ; idx, first = min(idx+step, lastIdx), false {
		started, spinUp, err := m.tryStart(ctx, distinct[idx])
		if err != nil {
			return result, err
		}
		if started {
			firstStartIdx = idx
			if first {
				fromStandstill[idx] = spinUp
			}
			break
		}
		lastFailIdx = idx
		if idx == lastIdx {
			break
		}
	}
	if firstStartIdx < 0 {
		ui.Warning("Fan %s: the fan didn't start from standstill at any PWM value, the breakaway PWM value is unknown", fan.GetId())
		return result, nil
	}

	// refine from standstill, then confirm the result from standstill: in the coarse ascent, a fan that started late
	// in one try is credited to the next one
	for {
		if _, ok := fromStandstill[firstStartIdx]; ok && firstStartIdx-lastFailIdx <= tolerance {
			break
		}
		stopped, err := m.stopAndWaitForStandstill(ctx, stopTarget)
		if err != nil {
			return result, err
		}
		if !stopped {
			ui.Warning("Fan %s: the fan didn't stop again at PWM %d, using PWM %d as the breakaway PWM value",
				fan.GetId(), stopTarget, distinct[firstStartIdx])
			break
		}
		probe := firstStartIdx
		if firstStartIdx-lastFailIdx > tolerance {
			probe = (lastFailIdx + firstStartIdx) / 2
		}
		started, spinUp, err := m.tryStart(ctx, distinct[probe])
		if err != nil {
			return result, err
		}
		switch {
		case started:
			firstStartIdx = probe
			fromStandstill[probe] = spinUp
		case probe == firstStartIdx && probe == lastIdx:
			ui.Warning("Fan %s: the fan didn't start from standstill at any PWM value, the breakaway PWM value is unknown", fan.GetId())
			return result, nil
		case probe == firstStartIdx:
			// the coarse result doesn't start the fan from standstill: move up
			lastFailIdx = probe
			firstStartIdx = min(probe+tolerance, lastIdx)
		default:
			lastFailIdx = probe
		}
	}

	breakaway := distinct[firstStartIdx]
	result.Pwm = &breakaway
	if spinUp, ok := fromStandstill[firstStartIdx]; ok {
		result.SpinUp = &spinUp
	}
	ui.Info("Fan %s: breakaway PWM value (start from standstill) detected at PWM %d (spin-up delay %s)",
		fan.GetId(), breakaway, util.Coerce(fromStandstill[firstStartIdx], 0, time.Hour).Round(time.Millisecond))
	return result, nil
}

// read reads the RPM of the fan and records when the reported value changes.
func (m *breakawayMeasurement) read() (int, error) {
	rpm, err := m.fan.GetRpm()
	if err != nil {
		ui.Warning("Fan %s: Cannot read RPM value: %v", m.fan.GetId(), err)
		return 0, err
	}
	if m.haveLastRpm && rpm != m.lastRpm {
		m.changeTimes = append(m.changeTimes, m.analyzer.clock().Now())
	}
	m.lastRpm = rpm
	m.haveLastRpm = true
	return rpm, nil
}

// tachInterval returns the median time between two changes of the reported RPM value, nil if there are fewer than
// minTachIntervalSamples intervals. A value that doesn't change at an update makes an interval longer, the median
// ignores a few of those.
func (m *breakawayMeasurement) tachInterval() *time.Duration {
	if len(m.changeTimes) < minTachIntervalSamples+1 {
		return nil
	}
	gaps := make([]time.Duration, 0, len(m.changeTimes)-1)
	for i := 1; i < len(m.changeTimes); i++ {
		gaps = append(gaps, m.changeTimes[i].Sub(m.changeTimes[i-1]))
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	median := gaps[len(gaps)/2]
	return &median
}

// tachIntervalOrDefault returns the tach interval measured so far, defaultTachInterval if it is unknown.
func (m *breakawayMeasurement) tachIntervalOrDefault() time.Duration {
	if interval := m.tachInterval(); interval != nil {
		return min(*interval, maxKickTimingValue)
	}
	return defaultTachInterval
}

// stopAndWaitForStandstill sets the fan to stopTarget and waits until it counts as stopped (see
// breakawayStandstillReadings), plus a pause of half its coast-down time. Returns false if the fan doesn't stop
// within analysis.settleTimeout (defaultBreakawayStandstillTimeout if that is 0).
func (m *breakawayMeasurement) stopAndWaitForStandstill(ctx context.Context, stopTarget int) (bool, error) {
	if err := m.analyzer.fanController.setTargetPwm(stopTarget); err != nil {
		return false, fmt.Errorf("unable to set PWM (target %d): %w", stopTarget, err)
	}
	clock := m.analyzer.clock()
	start := clock.Now()
	timeout := configuration.CurrentConfig.Analysis.SettleTimeout
	if timeout <= 0 {
		timeout = defaultBreakawayStandstillTimeout
	}
	deadline := start.Add(timeout)
	var stoppedSince time.Time
	stoppedReadings := 0
	for {
		if clock.Now().After(deadline) {
			return false, nil
		}
		if err := sleepWithContext(ctx, clock, breakawayPollInterval); err != nil {
			return false, err
		}
		rpm, err := m.read()
		if err != nil {
			stoppedReadings = 0
			continue
		}
		now := clock.Now()
		if fans.IsRpmLikelySpinning(float64(rpm)) {
			stoppedReadings = 0
			continue
		}
		if stoppedReadings == 0 {
			stoppedSince = now
		}
		stoppedReadings++
		span := max(minBreakawayStandstillSpan, 2*m.tachIntervalOrDefault())
		if stoppedReadings >= breakawayStandstillReadings && now.Sub(stoppedSince) >= span {
			break
		}
	}
	coastDown := stoppedSince.Sub(start)
	m.coastDown = max(m.coastDown, coastDown)
	pause := util.Coerce(coastDown/2, minBreakawayStandstillPause, maxBreakawayStandstillPause)
	return true, sleepWithContext(ctx, clock, pause)
}

// tryStart sets the fan to pwm (pre-pwmMap), waits until the fan reports it (up to fanResponseDelay), and returns
// true and the time from then to the first spinning RPM reading, if breakawayStartReadings readings in a row show the
// fan spinning, the first one within max(minBreakawayTryWindow, 3 tach intervals).
func (m *breakawayMeasurement) tryStart(ctx context.Context, pwm int) (bool, time.Duration, error) {
	controller := m.analyzer.fanController
	if err := controller.setTargetPwm(pwm); err != nil {
		return false, 0, fmt.Errorf("unable to set PWM (target %d): %w", pwm, err)
	}
	expectedPwm := controller.getReportedPwmAfterApplyingPwm(controller.applyPwmMapToTarget(pwm))
	if _, matched, _, err := controller.waitForReadback(ctx, func(reported int) bool { return reported == expectedPwm },
		configuration.CurrentConfig.FanResponseDelay.Duration()); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, 0, ctxErr
		}
		ui.Debug("Fan %s: unable to read back PWM %d: %v", m.fan.GetId(), pwm, err)
	} else if !matched {
		ui.Debug("Fan %s: the fan doesn't report PWM %d (yet), trying it anyway", m.fan.GetId(), pwm)
	}

	clock := m.analyzer.clock()
	start := clock.Now()
	window := max(minBreakawayTryWindow, breakawayTryWindowTachIntervals*m.tachIntervalOrDefault())
	deadline := start.Add(window)
	// a spinning reading just before the deadline gets the readings of up to 2 tach intervals to be confirmed
	hardDeadline := deadline.Add(2 * m.tachIntervalOrDefault())
	spinning := 0
	var spinUp time.Duration
	for now := clock.Now(); now.Before(hardDeadline) && (now.Before(deadline) || spinning > 0); now = clock.Now() {
		if err := sleepWithContext(ctx, clock, breakawayPollInterval); err != nil {
			return false, 0, err
		}
		rpm, err := m.read()
		if err != nil {
			// readings around a failed one aren't in a row
			spinning = 0
			continue
		}
		if !fans.IsRpmLikelySpinning(float64(rpm)) {
			spinning = 0
			if !clock.Now().Before(deadline) {
				break
			}
			continue
		}
		if spinning == 0 {
			spinUp = clock.Now().Sub(start)
		}
		spinning++
		// a single reading can be wrong: the fan started if breakawayStartReadings readings in a row show it spinning
		if spinning >= breakawayStartReadings {
			ui.Debug("Fan %s: started from standstill at PWM %d (%d RPM) after %s", m.fan.GetId(), pwm, rpm, spinUp)
			return true, spinUp, nil
		}
	}
	ui.Debug("Fan %s: didn't start from standstill at PWM %d within %s", m.fan.GetId(), pwm, window)
	return false, 0, nil
}
