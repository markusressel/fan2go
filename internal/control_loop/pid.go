package control_loop

import (
	"github.com/markusressel/fan2go/internal/util"
	"math"
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

func (l *PidControlLoop) Cycle(target int, lastSetPwm int) int {
	result := l.pidLoop.Loop(float64(target), float64(lastSetPwm))

	// ensure we are within sane bounds
	coerced := util.Coerce(float64(lastSetPwm)+result, 0, 255)
	stepTarget := int(math.Round(coerced))

	return stepTarget
}
