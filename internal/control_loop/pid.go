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

	subIntCumulativeError float64
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

	// if the result of the pid loop is in ]-1.0..1.0[ sum up values to ensure that we slowly creep up to the target, even
	// thought the actually applied value is an integer and not a float
	if result > -1.0 && result < 1.0 {
		l.subIntCumulativeError += result
	} else {
		// if the result is outside the range, reset the cumulative error
		l.subIntCumulativeError = 0
	}
	if l.subIntCumulativeError >= 1.0 || l.subIntCumulativeError <= -1.0 {
		// add the cumulative error to the result
		result += l.subIntCumulativeError
		// reset the cumulative error
		l.subIntCumulativeError = 0
	}

	newTarget := float64(current) + result
	// convert the result to an integer
	roundedTarget := int(math.Round(newTarget))
	// ensure we are within sane bounds
	coercedTarget := util.Coerce(roundedTarget, 0, 255)

	return coercedTarget
}
