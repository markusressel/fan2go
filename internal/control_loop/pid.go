package control_loop

import (
	"github.com/markusressel/fan2go/internal/util"
	"math"
)

type PidControlLoopDefaults struct {
	P float64
	I float64
	D float64
}

var (
	DefaultPidConfig = PidControlLoopDefaults{
		P: 0.3,
		I: 0.02,
		D: 0.005,
	}
)

// PidControlLoop is a PidLoop based control loop implementation.
type PidControlLoop struct {
	pidLoop *util.PidLoop
}

// NewPidControlLoop creates a PidControlLoop, which uses a PID loop to approach the target.
func NewPidControlLoop(
	p float64,
	i float64,
	d float64,
) *PidControlLoop {
	return &PidControlLoop{
		pidLoop: util.NewPidLoop(p, i, d),
	}
}

func (l *PidControlLoop) Cycle(target int, current int) int {
	result := l.pidLoop.Loop(float64(target), float64(current))

	// ensure we are within sane bounds
	coerced := util.Coerce(float64(current)+result, 0, 255)
	stepTarget := int(math.Round(coerced))

	return stepTarget
}
