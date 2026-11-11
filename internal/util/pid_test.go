package util

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

// fakeClock is a manually advanced time source, so tests control dt exactly instead of sleeping.
type fakeClock struct {
	t time.Time
}

func (c *fakeClock) now() time.Time {
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

// useFakeClock attaches a new fakeClock to the given loop and returns it.
func useFakeClock(pidLoop *PidLoop) *fakeClock {
	clock := &fakeClock{t: time.Unix(0, 0)}
	pidLoop.SetClock(clock.now)
	return clock
}

func TestNewPidLoop(t *testing.T) {
	// GIVEN
	p, i, d := 1.0, 2.0, 3.0

	// WHEN
	pidLoop := NewPidLoop(p, i, d, 0, 255, true, true)

	// THEN
	assert.Equal(t, p, pidLoop.p)
	assert.Equal(t, i, pidLoop.i)
	assert.Equal(t, d, pidLoop.d)
}

func TestPidLoop_Advance(t *testing.T) {
	// GIVEN
	p, i, d := 1.0, 2.0, 3.0
	pidLoop := NewPidLoop(p, i, d, 0, 255, true, true)

	// WHEN
	output := pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 5.0, output)

	// WHEN
	output = pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 5.0, output)
}

func TestPidLoop_P(t *testing.T) {
	// GIVEN
	p, i, d := 0.01, 0.0, 0.0
	pidLoop := NewPidLoop(p, i, d, 0, 255, true, true)
	clock := useFakeClock(pidLoop)

	// WHEN
	output := pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.05, output)

	clock.advance(time.Second)

	// WHEN
	output = pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.05, output)
}

func TestPidLoop_I(t *testing.T) {
	// GIVEN
	p, i, d := 0.0, 0.01, 0.0
	pidLoop := NewPidLoop(p, i, d, 0, 255, true, true)
	clock := useFakeClock(pidLoop)

	// WHEN
	output := pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.0, output)

	clock.advance(time.Second)

	// WHEN
	output = pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.InDelta(t, 0.0, output, 0.01)
}

func TestPidLoop_D(t *testing.T) {
	// GIVEN
	p, i, d := 0.0, 0.0, 0.01
	pidLoop := NewPidLoop(p, i, d, 0, 255, true, true)
	clock := useFakeClock(pidLoop)

	// WHEN
	output := pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.0, output)

	clock.advance(time.Second)

	// WHEN
	output = pidLoop.Loop(10.0, 8.0)
	// THEN
	assert.InDelta(t, 0, output, 0.01)
}

func TestPidLoop_CoolingGains_NoIntegralWindupWhileSaturatedAtMin(t *testing.T) {
	// GIVEN a cooling loop (negative gains) idling far below its setpoint for an hour
	pidLoop := NewPidLoop(-10, -0.5, 0, 0, 255, true, false) // as curves.NewSpeedCurve builds it
	clock := useFakeClock(pidLoop)
	pidLoop.Loop(80, 35)
	for n := 0; n < 3600; n++ {
		clock.advance(time.Second)
		assert.Equal(t, 0.0, pidLoop.Loop(80, 35))
	}

	// WHEN the temperature jumps above the setpoint
	clock.advance(time.Second)
	output := pidLoop.Loop(80, 90)

	// THEN the output responds at once instead of unwinding an hour of integral first
	assert.InDelta(t, 100.0, output, 0.01)
}

func TestPidLoop_CoolingGains_NoIntegralWindupWhileSaturatedAtMax(t *testing.T) {
	// GIVEN a cooling loop held at full output far above its setpoint for an hour
	pidLoop := NewPidLoop(-10, -0.5, 0, 0, 255, true, false) // as curves.NewSpeedCurve builds it
	clock := useFakeClock(pidLoop)
	pidLoop.Loop(80, 120)
	for n := 0; n < 3600; n++ {
		clock.advance(time.Second)
		assert.Equal(t, 255.0, pidLoop.Loop(80, 120))
	}

	// WHEN the temperature drops below the setpoint and stays there
	clock.advance(time.Second)
	output := pidLoop.Loop(80, 70)
	assert.Less(t, output, 255.0)
	for n := 0; n < 120; n++ {
		clock.advance(time.Second)
		output = pidLoop.Loop(80, 70)
	}

	// THEN the integral unwinds and the output reaches its minimum within two minutes
	assert.Equal(t, 0.0, output)
}

func TestPidLoop_PositiveGains_NoIntegralWindupWhileSaturatedAtMin(t *testing.T) {
	// GIVEN a loop with positive gains held at its minimum for an hour
	pidLoop := NewPidLoop(10, 0.5, 0, 0, 255, true, false) // as curves.NewSpeedCurve builds it
	clock := useFakeClock(pidLoop)
	pidLoop.Loop(35, 80)
	for n := 0; n < 3600; n++ {
		clock.advance(time.Second)
		assert.Equal(t, 0.0, pidLoop.Loop(35, 80))
	}

	// WHEN the error changes sign
	clock.advance(time.Second)
	output := pidLoop.Loop(90, 80)

	// THEN the output responds at once
	assert.InDelta(t, 100.0, output, 0.01)
}
