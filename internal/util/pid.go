package util

import (
	"time"
)

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
	// Anti-windup flag
	antiWindup bool
	// Anti-winddown flag
	antiWinddown bool

	// last measured value
	lastMeasured float64
	// integral from previous loop + error, i.e. integral error
	integral float64
	// last execution time of the loop
	lastTime time.Time
	// last output value
	lastOutput float64

	// now returns the current time, replaceable in tests
	now func() time.Time
}

func NewPidLoop(p, i, d, min, max float64, antiWindup, antiWinddown bool) *PidLoop {
	return &PidLoop{
		p:            p,
		i:            i,
		d:            d,
		outMin:       min,
		outMax:       max,
		antiWindup:   antiWindup,
		antiWinddown: antiWinddown,
		now:          time.Now,
	}
}

// SetClock replaces the time source used to compute the time between two Loop calls.
// Intended for tests, which can then advance time deterministically instead of sleeping.
func (p *PidLoop) SetClock(now func() time.Time) {
	p.now = now
}

// Reset makes the next Loop/LoopAt call initialize the loop again, as if it was new.
func (p *PidLoop) Reset() {
	p.lastTime = time.Time{}
	p.lastMeasured = 0
	p.integral = 0
	p.lastOutput = 0
}

// Preset initializes the loop as if it had been running in a steady state with the given output
// at the given measured value, so the next Loop/LoopAt call continues from there (bumpless transfer)
// instead of starting over from the P term alone. The integral is set so that the I term alone
// yields output (if the I gain is not 0). The time of the clock (see SetClock) is used as the time
// of the last loop.
func (p *PidLoop) Preset(output float64, measured float64) {
	p.lastTime = p.now()
	p.lastMeasured = measured
	p.lastOutput = output
	p.integral = 0
	if p.i != 0 {
		p.integral = output / p.i
	}
}

// Loop advances the pid loop, using the current time of its clock (see SetClock) as the time of the measurement
func (p *PidLoop) Loop(target float64, measured float64) float64 {
	return p.LoopAt(p.now(), target, measured)
}

// LoopAt advances the pid loop with a measurement taken at loopTime.
// The time between two measurements is used as dt; if it is not positive, the last output is returned.
func (p *PidLoop) LoopAt(loopTime time.Time, target float64, measured float64) float64 {
	initialized := !p.lastTime.IsZero()
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

	// Don't integrate if output is already saturated AND the error is trying to push it further.
	// The direction the integral pushes the output in is the sign of i*err, not of err:
	// with negative gains (e.g. a cooling curve), a positive error lowers the output.
	windupDetected := p.antiWindup && p.lastOutput >= p.outMax && p.i*err > 0
	winddownDetected := p.antiWinddown && p.lastOutput <= p.outMin && p.i*err < 0
	if !windupDetected && !winddownDetected {
		p.integral = p.integral + err*dt
	}
	// Keep the I term within the output range. Without this the integral grows without bound
	// while the output sits at a limit the guards above do not cover (e.g. a cooling curve with
	// negative gains idling far below its setpoint), and the output then lags for as long as
	// the integral took to build up.
	if p.i != 0 {
		p.integral = Coerce(p.i*p.integral, p.outMin, p.outMax) / p.i
	}

	// --- Update State for Next Loop ---
	p.lastTime = loopTime
	p.lastMeasured = measured
	p.lastOutput = clampedOutput

	return clampedOutput
}
