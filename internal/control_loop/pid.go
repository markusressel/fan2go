package control_loop

import (
	"time"

	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type PidControlLoopDefaults struct {
	P float64
	I float64
	D float64
}

var (
	DefaultPidConfig = PidControlLoopDefaults{
		P: 0.05,
		I: 0.4,
		D: 0.01,
	}
)

// defaultTickRate is the default fanController.adjustmentTickRate, used if no positive tick rate is given
const defaultTickRate = 200 * time.Millisecond

// minGapDt is the lower bound of the time a gap between two cycles counts as, see gapDt
const minGapDt = time.Second

// gapDt returns the time a gap between two cycles counts as: max(1s, tickRate). One tick would be safe, but it
// makes a fan whose every update is a gap (e.g. a slow fan) converge very slowly: about 28 updates to 90% of a
// step with the default values and a 200 ms tick, vs. 4 with 1 s. 1 s is well below the cycle time at which the
// default values start to oscillate (about 2.7 s), and overshoots a step down by at most about 8%.
func gapDt(tickRate time.Duration) time.Duration {
	return max(minGapDt, tickRate)
}

// minCycleGap is the lower bound of the gap between two cycles after which a control loop
// doesn't integrate over the time since the last cycle, see maxCycleGap.
const minCycleGap = 2 * time.Second

// maxCycleGapTicks is the number of ticks without a cycle after which a control loop
// doesn't integrate over the time since the last cycle, see maxCycleGap.
const maxCycleGapTicks = 5

// maxCycleGap returns the largest time between two cycles that a control loop with the given tick rate
// still integrates over: max(2s, 5 x tickRate). A longer gap means the loop was not cycled for a while
// (e.g. while the curve couldn't be evaluated, or the controller was restarted), or a cycle took that long
// (e.g. a slow fan). Integrating over it would saturate the output, so it counts as gapDt instead.
func maxCycleGap(tickRate time.Duration) time.Duration {
	return max(minCycleGap, maxCycleGapTicks*tickRate)
}

// PidControlLoop is a PidLoop based control loop implementation.
// The first cycle applies the target directly; the PID loop only smooths later changes of the target.
// A gap between two cycles that is longer than maxCycleGap counts as gapDt, so the loop doesn't integrate
// over the whole gap.
type PidControlLoop struct {
	pidLoop *util.PidLoop
	// the time a gap counts as, see gapDt
	gapDt time.Duration
	// the largest time between two cycles that is integrated over, see maxCycleGap
	maxGap time.Duration

	// true once a cycle has seeded the loop (with its target, or the preset value)
	initialized bool
	// the value the next cycle continues from (see Preset), or nil
	preset *float64
	// Store the last float64 output from the internal pidLoop (0-255 range)
	lastPidOutput float64
	// the time of the last cycle that advanced the loop
	lastCycleTime time.Time
}

// NewPidControlLoop creates a PidControlLoop, which uses a PID loop to approach the target.
// tickRate is the expected time between two cycles (fanController.adjustmentTickRate). It only determines
// how a gap between two cycles is handled (see maxCycleGap and gapDt).
func NewPidControlLoop(
	p float64,
	i float64,
	d float64,
	tickRate time.Duration,
) *PidControlLoop {
	if tickRate <= 0 {
		tickRate = defaultTickRate
	}
	return &PidControlLoop{
		pidLoop: util.NewPidLoop(p, i, d, 0, 255, true, true),
		gapDt:   gapDt(tickRate),
		maxGap:  maxCycleGap(tickRate),
	}
}

func (l *PidControlLoop) Cycle(now time.Time, target float64) float64 {
	if l.preset != nil {
		value := *l.preset
		l.preset = nil
		l.seed(now, value)
		ui.Debug("PidControlLoop: preset, continuing from: %.4f, target(curve): %.4f", l.lastPidOutput, target)
		return l.lastPidOutput
	}
	if !l.initialized {
		// first cycle: start at the target instead of ramping up from 0, which would slow the fan down
		// to (almost) its minimum on every start and config reload. Preset the PID loop as if it had
		// already settled at this value (bumpless transfer): its output at steady state comes from the
		// I term alone, so the next cycle continues from here instead of dropping back to ~0.
		l.seed(now, target)
		ui.Debug("PidControlLoop: first cycle, starting at target(curve): %.4f, result(float): %.4f", target, l.lastPidOutput)
		return l.lastPidOutput
	}
	if gap := now.Sub(l.lastCycleTime); gap < 0 || gap > l.maxGap {
		// the loop wasn't cycled for a while: integrating over the gap would saturate the I term and drive the
		// output to 0 or 255. Count the gap as gapDt instead, so the loop continues from where it was.
		// This also keeps the loop moving towards the target if every cycle takes longer than maxGap (e.g. a
		// slow fan), and if the clock went backwards.
		ui.Debug("PidControlLoop: %s since the last cycle, counting it as %s", gap, l.gapDt)
		l.pidLoop.ResumeAt(now.Add(-l.gapDt))
	}

	// Use the *previous output* of this PID loop as the 'measured' value for smoothing
	floatMeasured := l.lastPidOutput

	// Calculate the next PID output (clamped to [0..255] by pidLoop).
	// If no time passed since the last cycle, pidLoop returns its last output.
	floatResult := l.pidLoop.LoopAt(now, target, floatMeasured)

	// Store the *float* result as the internal state for the next cycle's feedback
	l.lastPidOutput = floatResult
	l.lastCycleTime = now

	ui.Debug("PidControlLoop: target(curve): %.4f, measured(lastPidOutput): %.4f, result(float): %.4f", target, floatMeasured, floatResult)

	return floatResult
}

// Reset makes the next cycle start at its target, like the first cycle.
func (l *PidControlLoop) Reset() {
	l.initialized = false
	l.preset = nil
}

// Preset makes the next cycle return value and continue smoothing from there.
func (l *PidControlLoop) Preset(value float64) {
	l.preset = &value
}

// seed sets the loop to a steady state at value (clamped to [0..255]) at the time now.
func (l *PidControlLoop) seed(now time.Time, value float64) {
	l.initialized = true
	l.lastPidOutput = util.Coerce(value, 0, 255)
	l.lastCycleTime = now
	l.pidLoop.PresetAt(now, l.lastPidOutput, l.lastPidOutput)
}
