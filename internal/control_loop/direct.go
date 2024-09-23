package control_loop

import (
	"github.com/markusressel/fan2go/internal/util"
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

func (l *DirectControlLoop) Loop(target float64, measured float64) float64 {
	loopTime := time.Now()

	dt := loopTime.Sub(l.lastTime).Seconds()

	l.lastTime = loopTime

	// the pwm adjustment depends on the direction and
	// the time-based change speed limit.
	maxPwmChangeThiStep := float64(l.maxPwmChangePerCycle) * dt
	err := target - measured
	// we can be above or below the target pwm value,
	// so we substract or add at most the max pwm change,
	// capped to having reached the target
	if err > 0 {
		// below desired speed, add pwms
		return util.Coerce(maxPwmChangeThiStep, 0, err)
	} else {
		// above or at desired speed, subtract pwms
		return util.Coerce(-maxPwmChangeThiStep, err, 0)
	}
}
