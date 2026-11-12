package control_loop

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// pidTestTick is the default fanController.adjustmentTickRate
const pidTestTick = 200 * time.Millisecond

// fakeNow is a manually advanced time source for the PID control loop.
type fakeNow struct {
	now time.Time
}

func (f *fakeNow) Now() time.Time { return f.now }

func (f *fakeNow) Advance(d time.Duration) { f.now = f.now.Add(d) }

func newDefaultPidLoopWithFakeClock() (*PidControlLoop, *fakeNow) {
	clock := &fakeNow{now: time.Unix(1000, 0)}
	loop := NewPidControlLoopWithClock(DefaultPidConfig.P, DefaultPidConfig.I, DefaultPidConfig.D, clock.Now)
	return loop, clock
}

// cycle runs the loop once and advances the clock by one tick afterwards.
func cycle(loop *PidControlLoop, clock *fakeNow, target float64) float64 {
	out := loop.Cycle(target)
	clock.Advance(pidTestTick)
	return out
}

func TestPidControlLoop_FirstCycles(t *testing.T) {
	// GIVEN
	loop, clock := newDefaultPidLoopWithFakeClock()

	// WHEN / THEN
	// the first cycle only uses the P term: P * (target - lastOutput) = 0.05 * (200 - 0)
	assert.InDelta(t, 10.0, cycle(loop, clock, 200), 1e-9)
	// second cycle (measured = 10): P 0.05*190 = 9.5, I 0 (the error is integrated only after
	// the output is computed), D -0.01*(10-0)/0.2 = -0.5 => 9.0; integral becomes 190*0.2 = 38
	assert.InDelta(t, 9.0, cycle(loop, clock, 200), 1e-9)
	// third cycle (measured = 9): P 0.05*191 = 9.55, I 0.4*38 = 15.2, D -0.01*(9-10)/0.2 = 0.05 => 24.8
	assert.InDelta(t, 24.8, cycle(loop, clock, 200), 1e-9)
}

func TestPidControlLoop_ConvergesToTarget(t *testing.T) {
	// GIVEN
	loop, clock := newDefaultPidLoopWithFakeClock()
	target := 200.0

	// WHEN
	outputs := make([]float64, 0, 100)
	for i := 0; i < 100; i++ {
		outputs = append(outputs, cycle(loop, clock, target))
	}

	// THEN
	// slow startup ramp with the default gains:
	// ~88 % of the target after 5 s (25 cycles), > 99 % after 12 s (60 cycles)
	assert.InDelta(t, 176.06, outputs[25], 0.01)
	assert.GreaterOrEqual(t, outputs[60], 0.99*target)
	for i := 60; i < len(outputs); i++ {
		assert.InDelta(t, target, outputs[i], 0.01*target, "cycle %d", i)
	}
	// no overshoot beyond the target
	for i, out := range outputs {
		assert.LessOrEqual(t, out, target, "cycle %d", i)
	}
}

func TestPidControlLoop_OutputStaysWithinPwmRange(t *testing.T) {
	tests := []struct {
		name    string
		p, i, d float64
		target  float64
	}{
		{name: "default gains, max target", p: DefaultPidConfig.P, i: DefaultPidConfig.I, d: DefaultPidConfig.D, target: 255},
		{name: "default gains, target above range", p: DefaultPidConfig.P, i: DefaultPidConfig.I, d: DefaultPidConfig.D, target: 400},
		{name: "default gains, target below range", p: DefaultPidConfig.P, i: DefaultPidConfig.I, d: DefaultPidConfig.D, target: -100},
		{name: "aggressive gains, max target", p: 5, i: 5, d: 0.5, target: 255},
		{name: "aggressive gains, target above range", p: 5, i: 5, d: 0.5, target: 1000},
		{name: "aggressive gains, target below range", p: 5, i: 5, d: 0.5, target: -1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := &fakeNow{now: time.Unix(1000, 0)}
			loop := NewPidControlLoopWithClock(tt.p, tt.i, tt.d, clock.Now)
			for i := 0; i < 200; i++ {
				out := cycle(loop, clock, tt.target)
				assert.GreaterOrEqual(t, out, 0.0, "cycle %d", i)
				assert.LessOrEqual(t, out, 255.0, "cycle %d", i)
			}
		})
	}
}

func TestPidControlLoop_TargetStepDown(t *testing.T) {
	// GIVEN: a loop that has converged to 200
	loop, clock := newDefaultPidLoopWithFakeClock()
	last := 0.0
	for i := 0; i < 80; i++ {
		last = cycle(loop, clock, 200)
	}
	assert.InDelta(t, 200, last, 1)

	// WHEN: the target steps down to 50
	outputs := make([]float64, 0, 100)
	for i := 0; i < 100; i++ {
		outputs = append(outputs, cycle(loop, clock, 50))
	}

	// THEN: the output decreases smoothly (no jump), monotonically and without undershoot
	assert.InDelta(t, 192.30, outputs[0], 0.01, "first cycle after the step only moves a little")
	prev := last
	for i, out := range outputs {
		assert.Less(t, out, prev+1e-9, "cycle %d", i)
		assert.GreaterOrEqual(t, out, 50.0, "cycle %d", i)
		prev = out
	}
	assert.InDelta(t, 50, outputs[len(outputs)-1], 0.02*200)
}

func TestPidControlLoop_NoTimePassed_ReturnsLastOutput(t *testing.T) {
	// GIVEN
	loop, clock := newDefaultPidLoopWithFakeClock()
	cycle(loop, clock, 200)
	cycle(loop, clock, 200)
	last := loop.Cycle(200)

	// WHEN: dt == 0
	sameTime := loop.Cycle(100)

	// THEN
	assert.Equal(t, last, sameTime)

	// WHEN: dt < 0 (clock went backwards)
	clock.Advance(-time.Second)
	backwards := loop.Cycle(100)

	// THEN
	assert.Equal(t, last, backwards)
}
