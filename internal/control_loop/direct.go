package control_loop

import (
	"math"
	"time"

	"github.com/markusressel/fan2go/internal/util"
)

// snapDistance is the distance to the target below which the low-pass filter jumps to the target.
// Without it, the filter would reach 0 or 255 only asymptotically.
const snapDistance = 0.5

// stopThreshold is the curve value below which UpdateFanSpeed stops a fan without neverStop.
// A change of the output across it is always applied, even within the deadband.
const stopThreshold = 1.0

// DirectControlLoopOptions are the optional stages of the DirectControlLoop. The zero value is a pass-through.
// All values are in curve units ([0..255]).
type DirectControlLoopOptions struct {
	// MaxPwmChangePerCycle limits the change of the output per cycle (in both directions), if set.
	// It replaces RampUp and RampDown (the config validation doesn't allow both).
	MaxPwmChangePerCycle *int
	// TimeConstant is the time constant of the first-order low-pass filter on the output (0 = off).
	TimeConstant time.Duration
	// RampUp is the maximum increase of the output per second (0 = unlimited).
	RampUp float64
	// RampDown is the maximum decrease of the output per second (0 = unlimited).
	RampDown float64
	// Deadband is the smallest change of the output that is applied (0 = off). A change that reaches
	// 0 or 255, or that starts or stops the fan (crosses stopThreshold), is always applied.
	Deadband float64
}

// DirectControlLoop applies the curve value, optionally smoothed in this order:
//
//  1. a first-order low-pass filter with TimeConstant,
//  2. a deadband: a change of the output smaller than Deadband is not applied,
//  3. a rate limit of RampUp/RampDown per second (or MaxPwmChangePerCycle per cycle).
//
// The output never moves away from the target. All stages except MaxPwmChangePerCycle use the time between
// two cycles, so they don't depend on the cycle rate. Without options, it applies the target directly.
type DirectControlLoop struct {
	options DirectControlLoopOptions

	// the last output, within [0..255] (NaN before the first cycle)
	lastOutput float64
	// the state of the low-pass filter, within [0..255]
	filtered float64
	// the time of the last cycle (zero: the next cycle doesn't advance the time-based stages)
	lastCycleTime time.Time
}

// NewDirectControlLoop creates a DirectControlLoop that applies the target directly, or, if
// maxPwmChangePerCycle is set, approaches it by at most that much per cycle (in both directions).
func NewDirectControlLoop(
	// (optional) limits the maximum allowed pwm change per cycle (in both directions)
	maxPwmChangePerCycle *int,
) *DirectControlLoop {
	return NewDirectControlLoopWithOptions(DirectControlLoopOptions{MaxPwmChangePerCycle: maxPwmChangePerCycle})
}

// NewDirectControlLoopWithOptions creates a DirectControlLoop with the given options.
func NewDirectControlLoopWithOptions(options DirectControlLoopOptions) *DirectControlLoop {
	return &DirectControlLoop{
		options:    options,
		lastOutput: math.NaN(),
	}
}

// Cycle applies the target (clamped to [0..255]) through the configured stages.
func (l *DirectControlLoop) Cycle(now time.Time, target float64) float64 {
	target = util.Coerce(target, 0, 255)

	if math.IsNaN(l.lastOutput) {
		// first run, just apply the target
		l.seed(target)
		l.lastCycleTime = now
		return l.lastOutput
	}

	dt := l.elapsed(now)
	l.lastCycleTime = now

	l.filtered = l.lowPass(target, dt)

	step := l.filtered - l.lastOutput
	if step == 0 || l.isWithinDeadband(step) {
		return l.lastOutput
	}
	if (target-l.lastOutput)*step <= 0 {
		// the filter lags behind a target that reversed: don't move away from the target
		return l.lastOutput
	}
	if limited := l.limitRate(step, dt); limited != step {
		l.lastOutput = util.Coerce(l.lastOutput+limited, 0, 255)
	} else {
		// exactly the filtered value (lastOutput + step may differ in the last bits)
		l.lastOutput = l.filtered
	}
	return l.lastOutput
}

// elapsed returns the time since the last cycle that counts for the time-based stages: the real time, also after
// a long pause (a low-pass or a rate limit can't saturate or oscillate, unlike pid; pauses in which the fan was set
// without the loop are handled by Preset and Reset). A clock that went backwards counts as no time.
func (l *DirectControlLoop) elapsed(now time.Time) time.Duration {
	if l.lastCycleTime.IsZero() {
		return 0
	}
	dt := now.Sub(l.lastCycleTime)
	if dt < 0 {
		return 0
	}
	return dt
}

// lowPass advances the first-order low-pass filter by dt towards target (exact discretization,
// so the result doesn't depend on how dt is split into cycles).
func (l *DirectControlLoop) lowPass(target float64, dt time.Duration) float64 {
	tau := l.options.TimeConstant
	if tau <= 0 {
		return target
	}
	alpha := 1 - math.Exp(-dt.Seconds()/tau.Seconds())
	filtered := l.filtered + alpha*(target-l.filtered)
	if math.Abs(target-filtered) < snapDistance {
		filtered = target
	}
	return filtered
}

// isWithinDeadband returns true if the change step of the output is too small to be applied.
func (l *DirectControlLoop) isWithinDeadband(step float64) bool {
	if l.options.Deadband <= 0 || math.Abs(step) >= l.options.Deadband {
		return false
	}
	next := l.lastOutput + step
	if next <= 0 || next >= 255 {
		// fan off and full speed are always reached
		return false
	}
	if (next < stopThreshold) != (l.lastOutput < stopThreshold) {
		// starting or stopping the fan is always applied
		return false
	}
	return true
}

// limitRate limits the change step of the output to the configured rate within dt.
func (l *DirectControlLoop) limitRate(step float64, dt time.Duration) float64 {
	if l.options.MaxPwmChangePerCycle != nil {
		maxChange := float64(*l.options.MaxPwmChangePerCycle)
		return util.Coerce(step, -maxChange, maxChange)
	}
	if step > 0 && l.options.RampUp > 0 {
		return min(step, l.options.RampUp*dt.Seconds())
	}
	if step < 0 && l.options.RampDown > 0 {
		return max(step, -l.options.RampDown*dt.Seconds())
	}
	return step
}

// seed sets the output and the filter to value (clamped to [0..255]).
func (l *DirectControlLoop) seed(value float64) {
	l.lastOutput = util.Coerce(value, 0, 255)
	l.filtered = l.lastOutput
}

// Reset makes the next cycle apply its target directly, like the first cycle.
func (l *DirectControlLoop) Reset() {
	l.lastOutput = math.NaN()
}

// Preset makes the next cycle continue from value. That cycle counts as no time, so the low-pass filter and
// the ramps return value and smooth from there on; maxPwmChangePerCycle (if set) limits the change from value.
// Without smoothing, the next cycle applies its target.
func (l *DirectControlLoop) Preset(value float64) {
	l.seed(value)
	l.lastCycleTime = time.Time{}
}
