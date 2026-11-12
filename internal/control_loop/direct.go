package control_loop

import (
	"math"
	"time"

	"github.com/markusressel/fan2go/internal/util"
)

// DirectControlLoop is a very simple control that directly applies the given
// target pwm. It can also be used to gracefully approach the target by
// utilizing the "maxPwmChangePerCycle" property.
type DirectControlLoop struct {
	// limits the maximum allowed pwm change per cycle
	maxPwmChangePerCycle *int

	// the last output, within [0..255] (NaN before the first cycle)
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
		lastOutput:           math.NaN(),
	}
}

// Cycle applies the target, limited by maxPwmChangePerCycle (if set). The time is not used.
func (l *DirectControlLoop) Cycle(_ time.Time, target float64) float64 {
	if math.IsNaN(l.lastOutput) {
		// first run, just apply the target (within sane bounds)
		l.lastOutput = util.Coerce(target, 0, 255)
		return l.lastOutput
	}

	var stepTarget = target
	if l.maxPwmChangePerCycle != nil {
		maxChangeValue := *l.maxPwmChangePerCycle

		err := target - l.lastOutput
		clampedErr := util.Coerce(err, -float64(maxChangeValue), +float64(maxChangeValue))
		stepTarget = l.lastOutput + clampedErr
	}

	// ensure we are within sane bounds
	result := util.Coerce(stepTarget, 0, 255)
	l.lastOutput = result

	return result
}

// Reset makes the next cycle apply its target directly, like the first cycle.
func (l *DirectControlLoop) Reset() {
	l.lastOutput = math.NaN()
}

// Preset makes the next cycle continue from value, so maxPwmChangePerCycle (if set) limits the change from it.
func (l *DirectControlLoop) Preset(value float64) {
	l.lastOutput = util.Coerce(value, 0, 255)
}
