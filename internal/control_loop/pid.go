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

// PidControlLoop is a PidLoop based control loop implementation.
// The first cycle applies the target directly; the PID loop only smooths later changes of the target.
type PidControlLoop struct {
	pidLoop *util.PidLoop

	// true once the first cycle has seeded the loop with its target
	initialized bool
	// Store the last float64 output from the internal pidLoop (0-255 range)
	lastPidOutput float64
}

// NewPidControlLoop creates a PidControlLoop, which uses a PID loop to approach the target.
func NewPidControlLoop(
	p float64,
	i float64,
	d float64,
) *PidControlLoop {
	return &PidControlLoop{
		pidLoop: util.NewPidLoop(p, i, d, 0, 255, true, true),
	}
}

// NewPidControlLoopWithClock creates a PidControlLoop like NewPidControlLoop, but uses the given
// time source to compute the time between two cycles. Intended for tests.
func NewPidControlLoopWithClock(
	p float64,
	i float64,
	d float64,
	now func() time.Time,
) *PidControlLoop {
	l := NewPidControlLoop(p, i, d)
	l.pidLoop.SetClock(now)
	return l
}

// Reset forgets all state, so the next Cycle starts at its target like the first cycle after creation
// (and presets the PID loop at the time of that cycle, so the time since the last cycle doesn't count).
func (l *PidControlLoop) Reset() {
	l.initialized = false
	l.lastPidOutput = 0
	l.pidLoop.Reset()
}

func (l *PidControlLoop) Cycle(target float64) float64 {
	if !l.initialized {
		// first cycle: start at the target instead of ramping up from 0, which would slow the fan down
		// to (almost) its minimum on every start and config reload. Preset the PID loop as if it had
		// already settled at this value (bumpless transfer): its output at steady state comes from the
		// I term alone, so the next cycle continues from here instead of dropping back to ~0.
		l.initialized = true
		l.lastPidOutput = util.Coerce(target, 0, 255)
		l.pidLoop.Preset(l.lastPidOutput, l.lastPidOutput)
		ui.Debug("PidControlLoop: first cycle, starting at target(curve): %.4f, result(float): %.4f", target, l.lastPidOutput)
		return l.lastPidOutput
	}

	// Convert the desired target value from the curve to float64
	floatTarget := target
	// Use the *previous output* of this PID loop as the 'measured' value for smoothing
	floatMeasured := l.lastPidOutput

	// Calculate the next PID output (float64, clamped 0-255 internally by pidLoop)
	floatResult := l.pidLoop.Loop(floatTarget, floatMeasured)

	// Store the *float* result as the internal state for the next cycle's feedback
	l.lastPidOutput = floatResult

	ui.Debug("PidControlLoop: target(curve): %.4f, measured(lastPidOutput): %.4f, result(float): %.4f", target, floatMeasured, floatResult)

	// ensure we are within sane bounds
	coercedTarget := util.Coerce(floatResult, 0, 255)

	return coercedTarget
}
