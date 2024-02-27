package util

import "time"

// an alternative loop control to gracefully
// approach the target in fan speed in a linear fashion by simply
// changing the pwm speed at most by x amount per cycle

type LinearLoop struct {
	pwmChangePerSecond int
	lastTime           time.Time
}

func NewLinearLoop(pwmChangePerSecond int) *LinearLoop {
	return &LinearLoop{
		pwmChangePerSecond: pwmChangePerSecond,
		lastTime:           time.Now(),
	}
}

func (l *LinearLoop) Loop(target float64, measured float64) float64 {
	loopTime := time.Now()

	dt := loopTime.Sub(l.lastTime).Seconds()

	l.lastTime = loopTime

	// the pwm adjustment depends on the direction and
	// the time-based change speed limit.
	maxPwmChangeThiStep := float64(l.pwmChangePerSecond) * dt
	err := target - measured
	// we can be above or below the target pwm value,
	// so we substract or add at most the max pwm change,
	// capped to having reached the target
	if err > 0 {
		// below desired speed, add pwms
		return Coerce(maxPwmChangeThiStep, 0, err)
	} else {
		// above or at desired speed, subtract pwms
		return Coerce(-maxPwmChangeThiStep, err, 0)
	}
}
