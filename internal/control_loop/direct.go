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
	maxPwmChangePerCycle int
	lastTime             time.Time
}

// NewDirectControlLoop creates a DirectControlLoop, which is a very simple control that directly applies the given
// target pwm. It can also be used to gracefully approach the target by
// utilizing the "maxPwmChangePerCycle" property.
func NewDirectControlLoop(
	// can be used to limit the maximum allowed pwm change per cycle
	maxPwmChangePerCycle int,
) *DirectControlLoop {
	return &DirectControlLoop{
		maxPwmChangePerCycle: maxPwmChangePerCycle,
		lastTime:             time.Now(),
	}
}

func (l *DirectControlLoop) Cycle(target int, measured int) int {
	loopTime := time.Now()

	dt := loopTime.Sub(l.lastTime).Seconds()

	l.lastTime = loopTime

	// the pwm adjustment depends on the direction and
	// the time-based change speed limit.
	maxPwmChangeThiStep := float64(l.maxPwmChangePerCycle) * dt
	err := float64(target - measured)
	// we can be above or below the target pwm value,
	// so we substract or add at most the max pwm change,
	// capped to having reached the target
	var stepTarget float64
	if err > 0 {
		// below desired speed, add pwms
		stepTarget = util.Coerce(maxPwmChangeThiStep, 0, err)
	} else {
		// above or at desired speed, subtract pwms
		stepTarget = util.Coerce(-maxPwmChangeThiStep, err, 0)
	}

	// ensure we are within sane bounds
	coerced := util.Coerce(stepTarget, 0, 255)
	result := int(math.Round(coerced))

	return result
}
