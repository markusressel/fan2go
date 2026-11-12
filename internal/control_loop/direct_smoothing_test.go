package control_loop

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const directTestTick = 200 * time.Millisecond

var directTestStart = time.Unix(1000, 0)

// runDirect cycles the loop with target every tick until (and including) the time end after the start,
// and returns the output of the last cycle.
func runDirect(loop *DirectControlLoop, start time.Time, tick time.Duration, end time.Duration, target float64) float64 {
	var output float64
	for elapsed := tick; elapsed <= end; elapsed += tick {
		output = loop.Cycle(start.Add(elapsed), target)
	}
	return output
}

func newSmoothingLoop(options DirectControlLoopOptions) *DirectControlLoop {
	return NewDirectControlLoopWithOptions(options)
}

func TestDirect_WithoutOptions_IsAPassThrough(t *testing.T) {
	// GIVEN
	loop := newSmoothingLoop(DirectControlLoopOptions{})
	now := directTestStart

	// THEN: every cycle applies its target, independent of the time
	for _, target := range []float64{100, 0, 255, 0.4, 37.5, 37.5, 300, -5} {
		now = now.Add(directTestTick)
		assert.Equal(t, math.Max(0, math.Min(255, target)), loop.Cycle(now, target))
	}
}

func TestDirect_LowPass_FollowsTheAnalyticStepResponse_AtEveryTickRate(t *testing.T) {
	tau := 2 * time.Second
	for _, tick := range []time.Duration{50 * time.Millisecond, 200 * time.Millisecond, time.Second, 2 * time.Second} {
		t.Run(tick.String(), func(t *testing.T) {
			// GIVEN: a settled output of 50
			loop := NewDirectControlLoopWithOptions(DirectControlLoopOptions{TimeConstant: tau})
			require.Equal(t, 50.0, loop.Cycle(directTestStart, 50))

			// WHEN: the target steps to 200
			previous := 50.0
			for elapsed := tick; elapsed <= 20*time.Second; elapsed += tick {
				output := loop.Cycle(directTestStart.Add(elapsed), 200)

				// THEN: the output equals 200 - 150 e^(-t/tau) (until it snaps to the target), without overshoot
				expected := 200 - 150*math.Exp(-elapsed.Seconds()/tau.Seconds())
				if 200-expected >= snapDistance {
					assert.InDelta(t, expected, output, 1e-9, "after %s", elapsed)
				} else {
					assert.Equal(t, 200.0, output, "after %s", elapsed)
				}
				assert.GreaterOrEqual(t, output, previous)
				assert.LessOrEqual(t, output, 200.0)
				previous = output
			}
		})
	}
}

func TestDirect_LowPass_IrregularTicksGiveTheSameTrajectory(t *testing.T) {
	// GIVEN
	options := DirectControlLoopOptions{TimeConstant: 2 * time.Second}
	regular := newSmoothingLoop(options)
	irregular := newSmoothingLoop(options)
	regular.Cycle(directTestStart, 200)
	irregular.Cycle(directTestStart, 200)
	random := rand.New(rand.NewSource(1))

	// WHEN: one loop is cycled every 200ms, the other at random times in between
	elapsed := time.Duration(0)
	for i := 1; i <= 50; i++ {
		next := time.Duration(i) * directTestTick
		for next-elapsed > 10*time.Millisecond {
			elapsed += time.Millisecond + time.Duration(random.Int63n(int64(next-elapsed-time.Millisecond)))
			irregular.Cycle(directTestStart.Add(elapsed), 50)
		}
		elapsed = next
		expected := regular.Cycle(directTestStart.Add(next), 50)

		// THEN: both outputs are the same at the regular times
		assert.InDelta(t, expected, irregular.Cycle(directTestStart.Add(next), 50), 1e-9)
	}
}

func TestDirect_LowPass_ReachesZeroAndFullSpeedExactly(t *testing.T) {
	// GIVEN
	loop := newSmoothingLoop(DirectControlLoopOptions{TimeConstant: 2 * time.Second})
	loop.Cycle(directTestStart, 100)

	// THEN: the filter snaps to the target instead of approaching it forever
	assert.Equal(t, 0.0, runDirect(loop, directTestStart, directTestTick, 30*time.Second, 0))
	assert.Equal(t, 255.0, runDirect(loop, directTestStart.Add(30*time.Second), directTestTick, 30*time.Second, 255))
}

func TestDirect_Ramps_LimitTheChangePerSecond_AtEveryTickRate(t *testing.T) {
	for _, tick := range []time.Duration{50 * time.Millisecond, 200 * time.Millisecond, 250 * time.Millisecond, time.Second} {
		t.Run(tick.String(), func(t *testing.T) {
			// GIVEN: up by at most 40 per second, down by at most 10 per second
			loop := NewDirectControlLoopWithOptions(DirectControlLoopOptions{RampUp: 40, RampDown: 10})
			loop.Cycle(directTestStart, 50)

			// THEN: 50 to 200 takes 150/40 = 3.75 s
			assert.InDelta(t, 50+40*3.0, runDirect(loop, directTestStart, tick, 3*time.Second, 200), 1e-9)
			assert.Equal(t, 200.0, runDirect(loop, directTestStart.Add(3*time.Second), tick, time.Second, 200))

			// THEN: 200 down to 150 takes 5 s
			start := directTestStart.Add(4 * time.Second)
			assert.InDelta(t, 200-10*4.0, runDirect(loop, start, tick, 4*time.Second, 150), 1e-9)
			assert.Equal(t, 150.0, runDirect(loop, start.Add(4*time.Second), tick, time.Second, 150))
		})
	}
}

func TestDirect_Ramps_OnlyOneDirectionLimited(t *testing.T) {
	// GIVEN: only the decrease is limited
	loop := newSmoothingLoop(DirectControlLoopOptions{RampDown: 5})
	loop.Cycle(directTestStart, 100)

	// THEN: an increase is applied at once, a decrease at 5 per second
	assert.Equal(t, 255.0, loop.Cycle(directTestStart.Add(directTestTick), 255))
	assert.InDelta(t, 254.0, loop.Cycle(directTestStart.Add(2*directTestTick), 0), 1e-9)
}

func TestDirect_Deadband_IgnoresSmallChanges(t *testing.T) {
	// GIVEN
	loop := newSmoothingLoop(DirectControlLoopOptions{Deadband: 2.55})
	now := directTestStart
	loop.Cycle(now, 100)

	// WHEN: the target jitters by less than the deadband
	for _, target := range []float64{101, 99, 102.5, 97.5, 100.3} {
		now = now.Add(directTestTick)
		// THEN: the output doesn't change
		assert.Equal(t, 100.0, loop.Cycle(now, target))
	}

	// WHEN/THEN: a change of at least the deadband is applied
	now = now.Add(directTestTick)
	assert.Equal(t, 102.6, loop.Cycle(now, 102.6))
	now = now.Add(directTestTick)
	assert.Equal(t, 90.0, loop.Cycle(now, 90))
}

func TestDirect_Deadband_AlwaysReachesZeroAndFullSpeed(t *testing.T) {
	// GIVEN
	loop := newSmoothingLoop(DirectControlLoopOptions{Deadband: 10})
	loop.Cycle(directTestStart, 250)

	// THEN
	assert.Equal(t, 255.0, loop.Cycle(directTestStart.Add(directTestTick), 255))
	assert.Equal(t, 255.0, loop.Cycle(directTestStart.Add(2*directTestTick), 250)) // held: 5 < 10
	loop.Preset(5)
	assert.Equal(t, 0.0, loop.Cycle(directTestStart.Add(3*directTestTick), 0))
	assert.Equal(t, 0.0, loop.Cycle(directTestStart.Add(4*directTestTick), 0.5))
}

func TestDirect_Deadband_StartingAndStoppingTheFanIsAlwaysApplied(t *testing.T) {
	// GIVEN: a fan that runs at curve value 2 (values < 1 stop a fan without neverStop)
	loop := newSmoothingLoop(DirectControlLoopOptions{Deadband: 2.55})
	loop.Cycle(directTestStart, 2)

	// THEN: a target below 1 stops it, a target of 1 or more starts it again, even within the deadband
	assert.Equal(t, 0.5, loop.Cycle(directTestStart.Add(directTestTick), 0.5))
	assert.Equal(t, 1.0, loop.Cycle(directTestStart.Add(2*directTestTick), 1))
	assert.Equal(t, 1.0, loop.Cycle(directTestStart.Add(3*directTestTick), 2))
}

func TestDirect_Deadband_HeldValueStaysWithinTheDeadbandOfTheTarget(t *testing.T) {
	// GIVEN
	loop := newSmoothingLoop(DirectControlLoopOptions{TimeConstant: 2 * time.Second, Deadband: 2.55})
	loop.Cycle(directTestStart, 50)

	// WHEN
	output := runDirect(loop, directTestStart, directTestTick, time.Minute, 200)

	// THEN: the output stops within the deadband below the target
	assert.LessOrEqual(t, output, 200.0)
	assert.Greater(t, output, 200-2.55)
}

func TestDirect_Deadband_SlowRampDoesNotFreezeTheOutput(t *testing.T) {
	// GIVEN: a ramp that changes the output by much less than the deadband per cycle
	loop := newSmoothingLoop(DirectControlLoopOptions{RampDown: 2.55, Deadband: 2.55})
	loop.Cycle(directTestStart, 200)

	// WHEN
	output := runDirect(loop, directTestStart, directTestTick, 10*time.Second, 100)

	// THEN: the output still moves at the ramp rate
	assert.InDelta(t, 200-25.5, output, 1e-9)
}

func TestDirect_Deadband_ReducesTheNumberOfChangesForANoisyTarget(t *testing.T) {
	count := func(options DirectControlLoopOptions) int {
		random := rand.New(rand.NewSource(42))
		loop := newSmoothingLoop(options)
		changes := 0
		last := loop.Cycle(directTestStart, 127)
		for elapsed := directTestTick; elapsed <= time.Minute; elapsed += directTestTick {
			// a noisy temperature on a steep curve: +-1 degree is +-6.4 curve units
			output := loop.Cycle(directTestStart.Add(elapsed), 127+6.4*math.Round(random.NormFloat64()*0.5))
			if math.Round(output) != math.Round(last) {
				changes++
			}
			last = output
		}
		return changes
	}

	direct := count(DirectControlLoopOptions{})
	smoothed := count(DirectControlLoopOptions{TimeConstant: 2 * time.Second, Deadband: 2.55})

	assert.Greater(t, direct, 50)
	assert.Less(t, smoothed, direct/4)
}

func TestDirect_NeverMovesAwayFromTheTarget(t *testing.T) {
	// GIVEN: a slow filter, which lags behind the target
	loop := newSmoothingLoop(DirectControlLoopOptions{TimeConstant: 10 * time.Second, RampDown: 1})
	loop.Cycle(directTestStart, 200)
	// the target drops: the filter goes down quickly, the output follows at 1 per second
	output := runDirect(loop, directTestStart, directTestTick, 10*time.Second, 0)
	require.InDelta(t, 190.0, output, 1e-9)

	// WHEN: the target goes back up above the output, while the filter is still below the output
	next := loop.Cycle(directTestStart.Add(10*time.Second+directTestTick), 250)

	// THEN: the output doesn't keep decreasing
	assert.Equal(t, output, next)
}

func TestDirect_UsesTheRealTimeSinceTheLastCycle(t *testing.T) {
	// GIVEN
	loop := newSmoothingLoop(DirectControlLoopOptions{RampUp: 10})
	loop.Cycle(directTestStart, 0)

	// WHEN: the next cycle comes 5 s later (e.g. a slow fan)
	output := loop.Cycle(directTestStart.Add(5*time.Second), 255)

	// THEN: all 5 s count, the ramp isn't slowed down
	assert.InDelta(t, 50.0, output, 1e-9)
}

func TestDirect_Deadband_PresetAtMaxThenFallingTarget(t *testing.T) {
	// GIVEN: the default options, settled at 100, then the fan was set to max without the loop
	loop := newSmoothingLoop(DirectControlLoopOptions{TimeConstant: 2 * time.Second, Deadband: 2.55})
	loop.Cycle(directTestStart, 100)
	loop.Preset(255)

	// WHEN: the target falls to 50 after the recovery
	start := directTestStart.Add(time.Minute)
	require.Equal(t, 255.0, loop.Cycle(start, 50))
	previous := 255.0
	var output float64
	for elapsed := directTestTick; elapsed <= 30*time.Second; elapsed += directTestTick {
		output = loop.Cycle(start.Add(elapsed), 50)

		// THEN: the output only decreases, never below the target, and leaves max at once
		assert.LessOrEqual(t, output, previous)
		assert.GreaterOrEqual(t, output, 50.0)
		if elapsed == directTestTick {
			assert.InDelta(t, 255-205*(1-math.Exp(-0.1)), output, 1e-9)
		}
		previous = output
	}
	// THEN: it ends within the deadband above the target
	assert.Less(t, output, 50+2.55)
}

func TestDirect_ClockGoingBackwardsCountsAsNoTime(t *testing.T) {
	// GIVEN
	loop := newSmoothingLoop(DirectControlLoopOptions{RampUp: 10})
	loop.Cycle(directTestStart, 0)

	// WHEN/THEN
	assert.Equal(t, 0.0, loop.Cycle(directTestStart.Add(-time.Minute), 255))
	assert.InDelta(t, 2.0, loop.Cycle(directTestStart.Add(-time.Minute+directTestTick), 255), 1e-9)
}

func TestDirect_Preset_SmoothsFromThePresetValue(t *testing.T) {
	// GIVEN: a settled output of 100
	loop := newSmoothingLoop(DirectControlLoopOptions{TimeConstant: 2 * time.Second, Deadband: 2.55})
	loop.Cycle(directTestStart, 100)
	runDirect(loop, directTestStart, directTestTick, 10*time.Second, 100)

	// WHEN: the fan was set to max without the loop, a minute later the loop continues
	loop.Preset(255)
	start := directTestStart.Add(70 * time.Second)

	// THEN: the next cycle returns the preset value, then the output comes down with the time constant
	assert.Equal(t, 255.0, loop.Cycle(start, 100))
	assert.InDelta(t, 100+155*math.Exp(-1), runDirect(loop, start, directTestTick, 2*time.Second, 100), 2.55)
	assert.Greater(t, runDirect(loop, start.Add(2*time.Second), directTestTick, time.Second, 100), 110.0)
}

func TestDirect_Preset_RampsFromThePresetValue(t *testing.T) {
	// GIVEN
	loop := newSmoothingLoop(DirectControlLoopOptions{RampDown: 10})
	loop.Cycle(directTestStart, 100)

	// WHEN
	loop.Preset(255)

	// THEN
	assert.Equal(t, 255.0, loop.Cycle(directTestStart.Add(time.Minute), 100))
	assert.InDelta(t, 253.0, loop.Cycle(directTestStart.Add(time.Minute+directTestTick), 100), 1e-9)
}

func TestDirect_Reset_StartsAtTheTarget(t *testing.T) {
	// GIVEN
	loop := newSmoothingLoop(DirectControlLoopOptions{TimeConstant: 2 * time.Second, RampDown: 1, Deadband: 2.55})
	loop.Cycle(directTestStart, 200)
	loop.Preset(255)

	// WHEN
	loop.Reset()

	// THEN
	assert.Equal(t, 30.0, loop.Cycle(directTestStart.Add(time.Second), 30))
}

func TestDirect_MaxPwmChangePerCycle_WithTimeConstant(t *testing.T) {
	// GIVEN: the deprecated per-cycle limit, together with a low-pass
	maxChange := 5
	loop := newSmoothingLoop(DirectControlLoopOptions{MaxPwmChangePerCycle: &maxChange, TimeConstant: time.Second})
	loop.Cycle(directTestStart, 0)

	// WHEN
	output := loop.Cycle(directTestStart.Add(directTestTick), 255)

	// THEN: the low-pass would move by 255 x (1 - e^-0.2) = 46, the limit allows 5 per cycle
	assert.Equal(t, 5.0, output)
}

func TestDirect_MaxPwmChangePerCycle_CountsCycles(t *testing.T) {
	// GIVEN
	maxChange := 10
	loop := newSmoothingLoop(DirectControlLoopOptions{MaxPwmChangePerCycle: &maxChange})
	loop.Cycle(directTestStart, 0)

	// THEN: independent of the time between two cycles
	assert.Equal(t, 10.0, loop.Cycle(directTestStart.Add(time.Millisecond), 255))
	assert.Equal(t, 20.0, loop.Cycle(directTestStart.Add(time.Minute), 255))
}
