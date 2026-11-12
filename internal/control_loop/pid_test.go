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

func TestPidControlLoop_FirstCycleStartsAtTarget(t *testing.T) {
	// GIVEN
	loop, clock := newDefaultPidLoopWithFakeClock()

	// WHEN / THEN
	// the first cycle applies the target directly, instead of ramping up from 0
	assert.Equal(t, 200.0, cycle(loop, clock, 200))
	// the PID loop was preset to that state (bumpless transfer), so it holds the output
	for i := 0; i < 100; i++ {
		assert.InDelta(t, 200.0, cycle(loop, clock, 200), 1, "cycle %d", i)
	}
}

func TestPidControlLoop_FirstCycleIsClamped(t *testing.T) {
	tests := []struct {
		name     string
		target   float64
		expected float64
	}{
		{name: "above range", target: 300, expected: 255},
		{name: "below range", target: -10, expected: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			loop, clock := newDefaultPidLoopWithFakeClock()

			// WHEN / THEN
			assert.Equal(t, tt.expected, cycle(loop, clock, tt.target))
			for i := 0; i < 20; i++ {
				assert.InDelta(t, tt.expected, cycle(loop, clock, tt.target), 1, "cycle %d", i)
			}
		})
	}
}

func TestPidControlLoop_TargetStepUpIsSmoothed(t *testing.T) {
	// GIVEN: a loop that started at 50
	loop, clock := newDefaultPidLoopWithFakeClock()
	assert.Equal(t, 50.0, cycle(loop, clock, 50))

	// WHEN: the target steps up to 200
	outputs := make([]float64, 0, 100)
	for i := 0; i < 100; i++ {
		outputs = append(outputs, cycle(loop, clock, 200))
	}

	// THEN: the output doesn't jump to the new target, but approaches it monotonically without overshoot.
	// First cycle after the step (measured = 50): P 0.05*(200-50) = 7.5, I 0.4*(50/0.4) = 50, D 0 => 57.5
	assert.InDelta(t, 57.5, outputs[0], 1e-9, "first cycle after the step only moves a little")
	prev := 50.0
	for i, out := range outputs {
		assert.Greater(t, out, prev-1e-9, "cycle %d", i)
		assert.LessOrEqual(t, out, 200.0, "cycle %d", i)
		prev = out
	}
	assert.InDelta(t, 200, outputs[len(outputs)-1], 0.02*200)
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
	// the loop started at 200 and held it exactly, so (measured = 200):
	// P 0.05*(50-200) = -7.5, I 0.4*(200/0.4) = 200, D 0 => 192.5
	assert.InDelta(t, 192.5, outputs[0], 1e-9, "first cycle after the step only moves a little")
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
