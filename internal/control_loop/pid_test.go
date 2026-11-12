package control_loop

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// pidTestTick is the default fanController.adjustmentTickRate
const pidTestTick = 200 * time.Millisecond

// fakeNow is a manually advanced time source for the tests of the PID control loop.
type fakeNow struct {
	now time.Time
}

func (f *fakeNow) Now() time.Time { return f.now }

func (f *fakeNow) Advance(d time.Duration) { f.now = f.now.Add(d) }

func newDefaultPidLoopWithFakeClock() (*PidControlLoop, *fakeNow) {
	clock := &fakeNow{now: time.Unix(1000, 0)}
	loop := NewPidControlLoop(DefaultPidConfig.P, DefaultPidConfig.I, DefaultPidConfig.D, pidTestTick)
	return loop, clock
}

// cycle runs the loop once and advances the clock by one tick afterwards.
func cycle(loop *PidControlLoop, clock *fakeNow, target float64) float64 {
	out := loop.Cycle(clock.Now(), target)
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
			loop := NewPidControlLoop(tt.p, tt.i, tt.d, pidTestTick)
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
	last := loop.Cycle(clock.Now(), 200)

	// WHEN: dt == 0
	sameTime := loop.Cycle(clock.Now(), 100)

	// THEN
	assert.Equal(t, last, sameTime)

	// WHEN: dt < 0 (clock went backwards)
	clock.Advance(-time.Second)
	backwards := loop.Cycle(clock.Now(), 100)

	// THEN
	assert.Equal(t, last, backwards)
}

// convergedPidLoop returns a loop with the default gains and the given tick rate, which has been running at value.
func convergedPidLoop(t *testing.T, tick time.Duration, value float64) (*PidControlLoop, *fakeNow) {
	clock := &fakeNow{now: time.Unix(1000, 0)}
	loop := NewPidControlLoop(DefaultPidConfig.P, DefaultPidConfig.I, DefaultPidConfig.D, tick)
	for i := 0; i < 20; i++ {
		assert.InDelta(t, value, loop.Cycle(clock.Now(), value), 1e-9)
		clock.Advance(tick)
	}
	return loop, clock
}

func TestMaxCycleGap(t *testing.T) {
	assert.Equal(t, 2*time.Second, maxCycleGap(0))
	assert.Equal(t, 2*time.Second, maxCycleGap(200*time.Millisecond))
	assert.Equal(t, 2*time.Second, maxCycleGap(400*time.Millisecond))
	assert.Equal(t, 5*time.Second, maxCycleGap(time.Second))
	assert.Equal(t, 10*time.Second, maxCycleGap(2*time.Second))
}

func TestPidControlLoop_GapDoesNotSaturateTheOutput(t *testing.T) {
	tests := []struct {
		name   string
		tick   time.Duration
		target float64
		// the default gains overshoot a little at a 1 s tick, independent of the gap
		tolerance float64
	}{
		{name: "target below, 200ms tick", tick: pidTestTick, target: 50, tolerance: 1e-9},
		{name: "target above, 200ms tick", tick: pidTestTick, target: 150, tolerance: 1e-9},
		{name: "target below, 1s tick", tick: time.Second, target: 50, tolerance: 5},
		{name: "target above, 1s tick", tick: time.Second, target: 150, tolerance: 5},
		{name: "target 0, 200ms tick", tick: pidTestTick, target: 0, tolerance: 1e-9},
		{name: "target 255, 200ms tick", tick: pidTestTick, target: 255, tolerance: 1e-9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN: a loop that has been running at 100
			loop, clock := convergedPidLoop(t, tt.tick, 100)

			// WHEN: it isn't cycled for 60 s, and then the target has changed
			clock.Advance(60 * time.Second)
			outputs := make([]float64, 0, 100)
			for i := 0; i < 100; i++ {
				outputs = append(outputs, loop.Cycle(clock.Now(), tt.target))
				clock.Advance(tt.tick)
			}

			// THEN: the loop continues from 100 and approaches the new target monotonically,
			// instead of jumping to 0 or 255 because it integrated the error over the gap
			assert.Equal(t, 100.0, outputs[0])
			low, high := min(100, tt.target), max(100, tt.target)
			prev := 100.0
			for i, out := range outputs {
				assert.GreaterOrEqual(t, out, low-tt.tolerance, "cycle %d", i)
				assert.LessOrEqual(t, out, high+tt.tolerance, "cycle %d", i)
				if tt.target < 100 {
					assert.LessOrEqual(t, out, prev+tt.tolerance, "cycle %d", i)
				} else {
					assert.GreaterOrEqual(t, out, prev-tt.tolerance, "cycle %d", i)
				}
				prev = out
			}
			assert.NotEqual(t, 100.0, outputs[len(outputs)-1], "the loop still follows the target after the gap")
		})
	}
}

func TestPidControlLoop_GapThreshold(t *testing.T) {
	tests := []struct {
		name     string
		tick     time.Duration
		gap      time.Duration
		reseeded bool
	}{
		{name: "200ms tick, gap of 2s is integrated", tick: pidTestTick, gap: 2 * time.Second, reseeded: false},
		{name: "200ms tick, gap above 2s re-seeds", tick: pidTestTick, gap: 2*time.Second + time.Millisecond, reseeded: true},
		{name: "1s tick, gap of 5s is integrated", tick: time.Second, gap: 5 * time.Second, reseeded: false},
		{name: "1s tick, gap above 5s re-seeds", tick: time.Second, gap: 5*time.Second + time.Millisecond, reseeded: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			loop, clock := convergedPidLoop(t, tt.tick, 100)

			// WHEN
			clock.Advance(tt.gap - tt.tick)
			out := loop.Cycle(clock.Now(), 50)

			// THEN
			if tt.reseeded {
				assert.Equal(t, 100.0, out)
			} else {
				// P 0.05*(50-100) = -2.5, I = 100, D 0
				assert.InDelta(t, 97.5, out, 1e-9)
			}
		})
	}
}

func TestPidControlLoop_Preset(t *testing.T) {
	// GIVEN: a loop that has been running at 100
	loop, clock := convergedPidLoop(t, pidTestTick, 100)

	// WHEN: the fan was set to max without the loop, the loop is preset there, and then the target is 100 again
	clock.Advance(30 * time.Second)
	loop.Preset(255)
	outputs := make([]float64, 0, 100)
	for i := 0; i < 100; i++ {
		outputs = append(outputs, cycle(loop, clock, 100))
	}

	// THEN: the loop continues from 255 and comes down smoothly, without going below the target
	assert.Equal(t, 255.0, outputs[0])
	assert.InDelta(t, 255-0.05*155, outputs[1], 1e-9, "the second cycle only moves a little")
	prev := 255.0
	for i, out := range outputs {
		assert.LessOrEqual(t, out, prev+1e-9, "cycle %d", i)
		assert.GreaterOrEqual(t, out, 100.0, "cycle %d", i)
		prev = out
	}
	assert.InDelta(t, 100, outputs[len(outputs)-1], 3)
}

func TestPidControlLoop_PresetIsClamped(t *testing.T) {
	// GIVEN
	loop, clock := newDefaultPidLoopWithFakeClock()

	// WHEN
	loop.Preset(300)

	// THEN
	assert.Equal(t, 255.0, cycle(loop, clock, 255))
	assert.InDelta(t, 255.0, cycle(loop, clock, 255), 1e-9)
}

func TestPidControlLoop_PresetBeforeTheFirstCycle(t *testing.T) {
	// GIVEN
	loop, clock := newDefaultPidLoopWithFakeClock()

	// WHEN
	loop.Preset(255)

	// THEN: the first cycle starts at the preset value, not at the target
	assert.Equal(t, 255.0, cycle(loop, clock, 100))
	assert.Less(t, cycle(loop, clock, 100), 255.0)
}

func TestPidControlLoop_Reset(t *testing.T) {
	// GIVEN: a loop that has been running at 100, and was preset
	loop, clock := convergedPidLoop(t, pidTestTick, 100)
	loop.Preset(255)

	// WHEN
	loop.Reset()

	// THEN: the next cycle starts at its target, like the first cycle, and holds it
	assert.Equal(t, 80.0, cycle(loop, clock, 80))
	for i := 0; i < 20; i++ {
		assert.InDelta(t, 80.0, cycle(loop, clock, 80), 1e-9, "cycle %d", i)
	}
}

func TestPidControlLoop_ClockGoesBackwards_LoopKeepsRunning(t *testing.T) {
	// GIVEN
	loop, clock := convergedPidLoop(t, pidTestTick, 100)

	// WHEN: the clock goes back by an hour
	clock.Advance(-time.Hour)

	// THEN: the loop holds its output once, and then follows the target again
	assert.Equal(t, 100.0, cycle(loop, clock, 50))
	assert.InDelta(t, 97.5, cycle(loop, clock, 50), 1e-9)
}
