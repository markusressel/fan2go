package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/ui"
)

const (
	// breakawayAttemptTimeout is the time a fan gets to start from standstill at a PWM value during the breakaway
	// measurement
	breakawayAttemptTimeout = 4 * time.Second
	// breakawayStandstillReadings is the number of RPM readings in a row below the spin threshold after which a
	// fan counts as stopped during the breakaway measurement
	breakawayStandstillReadings = 4
	// breakawayStandstillPause is the extra time a fan gets after it counts as stopped: a slowly coasting fan can
	// read 0 RPM already, and would start more easily
	breakawayStandstillPause = 2 * time.Second
	// defaultBreakawayStandstillTimeout is the longest wait for a fan to stop, if analysis.settleTimeout is 0
	defaultBreakawayStandstillTimeout = 30 * time.Second
	// breakawayCoarseSteps is the number of steps the coarse search of the breakaway measurement divides the
	// distinct PWM values into. The refinement stops at a quarter of a step.
	breakawayCoarseSteps = 16
)

// measureBreakawayPwm measures the lowest PWM value (pre-pwmMap) that starts the fan from standstill (the breakaway
// PWM), which can be much higher than the start boundary (the lowest value that keeps a spinning fan turning).
// startIdx is the index of the start boundary in distinct. It stops the fan, then tries the start boundary and every
// breakawayCoarseSteps-th distinct value above it without stopping the fan in between, until one starts the fan
// (spins within breakawayAttemptTimeout). Then it refines between the last value that didn't start the fan and the
// first that did by bisection, from standstill each time, down to a quarter of a coarse step. Returns nil if the fan
// doesn't stop (it spins at the lowest value, or doesn't stop within the settle timeout) or doesn't start at all.
func (f *FanCurveAnalyzer) measureBreakawayPwm(ctx context.Context, fan fans.Fan, distinct []int, startIdx int) (*int, error) {
	if startIdx <= 0 || startIdx >= len(distinct) {
		ui.Info("Fan %s: the fan spins at its lowest PWM value, skipping the breakaway measurement", fan.GetId())
		return nil, nil
	}
	ui.Info("Fan %s: Measuring the breakaway PWM value (the lowest PWM value that starts the fan from standstill)...", fan.GetId())

	lastIdx := len(distinct) - 1
	step := max(1, ceilDiv(len(distinct), breakawayCoarseSteps))
	tolerance := max(1, step/4)
	stopTarget := distinct[0]

	stopped, err := f.stopAndWaitForStandstill(ctx, fan, stopTarget)
	if err != nil {
		return nil, err
	}
	if !stopped {
		ui.Warning("Fan %s: the fan didn't stop at PWM %d, skipping the breakaway measurement", fan.GetId(), stopTarget)
		return nil, nil
	}

	// the start boundary is the lowest value that keeps the fan spinning: the value below it can't start it
	lastFailIdx := startIdx - 1
	firstStartIdx := -1
	for idx := startIdx; ; idx = min(idx+step, lastIdx) {
		started, err := f.tryStartFromStandstill(ctx, fan, distinct[idx])
		if err != nil {
			return nil, err
		}
		if started {
			firstStartIdx = idx
			break
		}
		lastFailIdx = idx
		if idx == lastIdx {
			break
		}
	}
	if firstStartIdx < 0 {
		ui.Warning("Fan %s: the fan didn't start from standstill at any PWM value, the breakaway PWM value is unknown", fan.GetId())
		return nil, nil
	}

	for firstStartIdx-lastFailIdx > tolerance {
		stopped, err := f.stopAndWaitForStandstill(ctx, fan, stopTarget)
		if err != nil {
			return nil, err
		}
		if !stopped {
			// keep the coarse result, it started the fan
			break
		}
		mid := (lastFailIdx + firstStartIdx) / 2
		started, err := f.tryStartFromStandstill(ctx, fan, distinct[mid])
		if err != nil {
			return nil, err
		}
		if started {
			firstStartIdx = mid
		} else {
			lastFailIdx = mid
		}
	}

	breakaway := distinct[firstStartIdx]
	ui.Info("Fan %s: breakaway PWM value (start from standstill) detected at PWM %d", fan.GetId(), breakaway)
	return &breakaway, nil
}

// stopAndWaitForStandstill sets the fan to stopTarget and waits until breakawayStandstillReadings RPM readings in a
// row are below the spin threshold, plus breakawayStandstillPause. Returns false if the fan doesn't stop within
// analysis.settleTimeout (defaultBreakawayStandstillTimeout if that is 0).
func (f *FanCurveAnalyzer) stopAndWaitForStandstill(ctx context.Context, fan fans.Fan, stopTarget int) (bool, error) {
	if err := f.fanController.setTargetPwm(stopTarget); err != nil {
		return false, fmt.Errorf("unable to set PWM (target %d): %w", stopTarget, err)
	}
	clock := f.clock()
	timeout := configuration.CurrentConfig.Analysis.SettleTimeout
	if timeout <= 0 {
		timeout = defaultBreakawayStandstillTimeout
	}
	deadline := clock.Now().Add(timeout)
	stoppedReadings := 0
	for stoppedReadings < breakawayStandstillReadings {
		if clock.Now().After(deadline) {
			return false, nil
		}
		if err := sleepWithContext(ctx, clock, f.sampleInterval()); err != nil {
			return false, err
		}
		rpm, err := fan.GetRpm()
		if err != nil {
			ui.Warning("Fan %s: Cannot read RPM value: %v", fan.GetId(), err)
			stoppedReadings = 0
			continue
		}
		if fans.IsRpmLikelySpinning(float64(rpm)) {
			stoppedReadings = 0
		} else {
			stoppedReadings++
		}
	}
	return true, sleepWithContext(ctx, clock, breakawayStandstillPause)
}

// tryStartFromStandstill sets the fan to pwm (pre-pwmMap) and returns true as soon as an RPM reading shows it
// spinning, false if none does within breakawayAttemptTimeout.
func (f *FanCurveAnalyzer) tryStartFromStandstill(ctx context.Context, fan fans.Fan, pwm int) (bool, error) {
	if err := f.fanController.setTargetPwm(pwm); err != nil {
		return false, fmt.Errorf("unable to set PWM (target %d): %w", pwm, err)
	}
	clock := f.clock()
	deadline := clock.Now().Add(breakawayAttemptTimeout)
	for clock.Now().Before(deadline) {
		if err := sleepWithContext(ctx, clock, f.sampleInterval()); err != nil {
			return false, err
		}
		rpm, err := fan.GetRpm()
		if err != nil {
			ui.Warning("Fan %s: Cannot read RPM value: %v", fan.GetId(), err)
			continue
		}
		if fans.IsRpmLikelySpinning(float64(rpm)) {
			ui.Debug("Fan %s: started from standstill at PWM %d (%d RPM)", fan.GetId(), pwm, rpm)
			return true, nil
		}
	}
	ui.Debug("Fan %s: didn't start from standstill at PWM %d within %s", fan.GetId(), pwm, breakawayAttemptTimeout)
	return false, nil
}

// sampleInterval returns the time between two RPM readings while waiting for the fan.
func (f *FanCurveAnalyzer) sampleInterval() time.Duration {
	if f.settleSampleInterval <= 0 {
		return defaultSettleSampleInterval
	}
	return f.settleSampleInterval
}
