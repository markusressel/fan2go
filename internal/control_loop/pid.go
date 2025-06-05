package control_loop

import (
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
type PidControlLoop struct {
	pidLoop *util.PidLoop

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

func (l *PidControlLoop) Cycle(target float64) float64 {
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
