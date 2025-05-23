package control_loop

import (
	"github.com/markusressel/fan2go/internal/util"
	"math"
	"time"
)

// DirectControlLoop is a very simple control that directly applies the given
// target pwm. It can also be used to gracefully approach the target by
// utilizing the "maxPwmChangePerCycle" property.
type DirectControlLoop struct {
	// limits the maximum allowed pwm change per cycle
	maxPwmChangePerCycle *int
	lastTime             time.Time

	lastOutput float64
}

// NewDirectControlLoop creates a DirectControlLoop, which is a very simple control that directly applies the given
// target pwm. It can also be used to gracefully approach the target by
// utilizing the "maxPwmChangePerCycle" property.
func NewDirectControlLoop(
// (optional) limits the maximum allowed pwm change per cycle (in both directions)
	maxPwmChangePerCycle *int,
) *DirectControlLoop {
	return &DirectControlLoop{
		maxPwmChangePerCycle: maxPwmChangePerCycle,
		lastTime:             time.Now(),
		lastOutput:           math.NaN(),
	}
}

func (l *DirectControlLoop) Cycle(target int) int {
	loopTime := time.Now()
	targetFloat := float64(target)
	if math.IsNaN(l.lastOutput) {
		// first run, just return the target
		l.lastOutput = targetFloat
		l.lastTime = loopTime
		return target
	}

	var stepTarget = targetFloat
	if l.maxPwmChangePerCycle != nil {
		maxChangeValue := *l.maxPwmChangePerCycle

		err := targetFloat - l.lastOutput
		clampedErr := util.Coerce(err, -float64(maxChangeValue), +float64(maxChangeValue))
		stepTarget = l.lastOutput + clampedErr
	}

	l.lastOutput = stepTarget

	// convert the result to an integer
	rounded := int(math.Round(stepTarget))
	// ensure we are within sane bounds
	result := util.Coerce(rounded, 0, 255)

	return result
}
