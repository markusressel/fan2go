package control_loop

import (
	"math"
	"time"
)

// PidOvershootThreshold is the overshoot of the step response (relative to the step) above which
// AnalyzePidStability reports a PID control loop as overshooting.
const PidOvershootThreshold = 0.01

// PidStability is the result of AnalyzePidStability.
type PidStability struct {
	// Stable is true if all poles of the loop are strictly inside the unit circle.
	Stable bool
	// Overshoot is the largest overshoot of the step response, relative to the step
	// (0.1 means the output overshoots a change of the target by 10 %). Only set if Stable.
	Overshoot float64
}

// Overshoots returns true if the loop is stable, but its step response overshoots by more than
// PidOvershootThreshold.
func (s PidStability) Overshoots() bool {
	return s.Stable && s.Overshoot > PidOvershootThreshold
}

// AnalyzePidStability analyzes the PidControlLoop with the given gains, advanced every dt.
//
// The loop uses its own previous output as the measured value, so (inside the 0..255 output range,
// i.e. without clamping and anti-windup) it is a linear discrete system of its own. With the target r,
// the output y, the I term u and T = dt in seconds, one cycle of util.PidLoop computes:
//
//	y[k] = p*(r - y[k-1]) + u[k-1] - (d/T)*(y[k-1] - y[k-2])
//	u[k] = u[k-1] + i*T*(r - y[k-1])
//
// Its characteristic polynomial is
//
//	z³ + (p + d/T - 1)*z² + (i*T - p - 2*d/T)*z + d/T
//
// The loop is stable if all roots are strictly inside the unit circle, which is checked with the
// Jury stability criterion. P(1) = i*T, so the loop is never stable with i <= 0.
// The overshoot is measured on the step response, starting from the settled state the first cycle
// of PidControlLoop presets the loop to.
func AnalyzePidStability(p, i, d float64, dt time.Duration) PidStability {
	t := dt.Seconds()
	if t <= 0 || !isFinite(p) || !isFinite(i) || !isFinite(d) {
		return PidStability{}
	}
	b := d / t
	c := i * t

	// monic polynomial z³ + a2*z² + a1*z + a0
	a2 := p + b - 1
	a1 := c - p - 2*b
	a0 := b

	stable := c > 0 && // P(1) > 0
		-1+a2-a1+a0 < 0 && // (-1)³ * P(-1) > 0
		math.Abs(a0) < 1 &&
		math.Abs(a0*a0-1) > math.Abs(a0*a2-a1)
	if !stable {
		return PidStability{}
	}

	return PidStability{Stable: true, Overshoot: stepResponseOvershoot(p, b, c)}
}

// stepResponseOvershoot simulates the linear loop (see AnalyzePidStability) for a step of the target
// from 0 to 1, starting at rest, and returns how far the output exceeds 1 (0 if it never does).
// Only call this for a stable loop, it converges then. With poles very close to 1 (a tiny i) this takes up
// to maxSteps iterations of a few floating point operations, which is still cheap enough for validation.
func stepResponseOvershoot(p, b, c float64) float64 {
	const target = 1.0
	const maxSteps = 1_000_000
	const epsilon = 1e-9

	yPrev, yPrevPrev, u := 0.0, 0.0, 0.0
	peak := 0.0
	for k := 0; k < maxSteps; k++ {
		e := target - yPrev
		y := p*e + u - b*(yPrev-yPrevPrev)
		u += c * e
		yPrevPrev, yPrev = yPrev, y
		if y > peak {
			peak = y
		}
		if math.Abs(y-target) < epsilon && math.Abs(yPrevPrev-target) < epsilon && math.Abs(u-target) < epsilon {
			break
		}
	}
	return math.Max(0, peak-target)
}

func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}
