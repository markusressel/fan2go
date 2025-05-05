package util

import "time"

type PidLoop struct {
	// Proptional Constant
	p float64
	// Integral Constant
	i float64
	// Derivative Constant
	d float64

	// last measured value
	lastMeasured float64

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
	output := 0.0

	loopTime := time.Now()
	if p.lastTime.IsZero() {
		p.lastMeasured = measured
		p.lastTime = loopTime
		return target
	} else {
		timeSinceLastLoop := loopTime.Sub(p.lastTime)
		dt := timeSinceLastLoop.Seconds()

		err := target - measured
		p.error = err

		proportional := err
		p.integral = p.integral + err*dt

		// avoid derivative kick
		derivative := (measured - p.lastMeasured) / dt
		output = p.p*proportional + p.i*p.integral - p.d*derivative
	}

	p.lastTime = loopTime
	p.lastMeasured = measured

	return output
}
