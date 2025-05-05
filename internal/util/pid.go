package util

import "time"

type PidLoop struct {
	// Proportional Constant
	p float64
	// Integral Constant
	i float64
	// Derivative Constant
	d float64
	// Minimum output value
	outMin float64
	// Maximum output value
	outMax float64

	// last measured value
	lastMeasured float64
	// integral from previous loop + error, i.e. integral error
	integral float64
	// last execution time of the loop
	lastTime time.Time
	// last output value
	lastOutput float64
}

func NewPidLoop(p, i, d, min, max float64) *PidLoop {
	return &PidLoop{
		p:      p,
		i:      i,
		d:      d,
		outMin: min,
		outMax: max,
	}
}

// Loop advances the pid loop
func (p *PidLoop) Loop(target float64, measured float64) float64 {
	initialized := !p.lastTime.IsZero()
	loopTime := time.Now()
	if !initialized {
		p.lastMeasured = measured
		p.lastTime = loopTime
		p.integral = 0.0 // Start integral cleanly

		// Calculate initial output (e.g., P-term only, clamped)
		initialError := target - measured
		output := p.p * initialError
		if output > p.outMax {
			output = p.outMax
		}
		if output < p.outMin {
			output = p.outMin
		}
		p.lastOutput = output // Store the clamped initial output
		return output
	}

	timeSinceLastLoop := loopTime.Sub(p.lastTime)
	dt := timeSinceLastLoop.Seconds()

	// Handle potential division by zero or weirdness if dt is not positive
	if dt <= 0 {
		return p.lastOutput // Return last known good output if no time passed
	}

	err := target - measured

	// --- P Term ---
	proportionalTerm := p.p * err

	// --- I Term (with basic anti-windup) ---
	integrate := true
	// Don't integrate if output is already saturated AND the error is trying to push it further
	if p.lastOutput >= p.outMax && err > 0 {
		integrate = false
	}
	if p.lastOutput <= p.outMin && err < 0 {
		integrate = false
	}

	if integrate {
		p.integral = p.integral + err*dt
	}
	integralTerm := p.i * p.integral

	// --- D Term (on measurement) ---
	// avoid derivative kick
	derivativeRaw := (measured - p.lastMeasured) / dt
	derivativeTerm := -p.d * derivativeRaw // Note the minus sign

	// --- Combine Terms ---
	output := proportionalTerm + integralTerm + derivativeTerm

	// --- Clamp Output ---
	clampedOutput := output
	if clampedOutput > p.outMax {
		clampedOutput = p.outMax
	} else if clampedOutput < p.outMin {
		clampedOutput = p.outMin
	}

	// --- Update State for Next Loop ---
	p.lastTime = loopTime
	p.lastMeasured = measured
	p.lastOutput = clampedOutput

	return clampedOutput
}
