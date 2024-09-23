package util

import (
	"time"
)

type PidLoop struct {
	// Proptional Constant
	p float64
	// Integral Constant
	i float64
	// Derivative Constant
	d float64

	// error from previous loop
	error float64
	// integral from previous loop + error, i.e. integral error
	integral float64
	// error - error from previous loop, i.e. differential error
	//differentialError float64
	// last execution time of the loop
	lastTime time.Time
}

func NewPidLoop(p float64, i float64, d float64) *PidLoop {
	return &PidLoop{
		p: p,
		i: i,
		d: d,
	}
}

// Loop advances the pid loop
func (p *PidLoop) Loop(target float64, measured float64) float64 {
	// TODO: make user configurable
	const maxPwmChangePerCycle float64 = 10.0

	loopTime := time.Now()
	var dt float64
	if p.lastTime.IsZero() {
		dt = 1
	} else {
		dt = loopTime.Sub(p.lastTime).Seconds()
	}
	p.lastTime = loopTime

	// the pwm adjustment depends on the direction and
	// the time-based change speed limit.
	maxPwmAdjustmentThiStep := maxPwmChangePerCycle * dt
	err := target - measured
	if err > 0 {
		return Coerce(maxPwmAdjustmentThiStep, 0, err)
	} else if err < 0 {
		return Coerce(-maxPwmAdjustmentThiStep, err, 0)
	} else {
		return 0
	}
}
