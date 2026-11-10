package util

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

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

	// WHEN
	output := pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.05, output)

	time.Sleep(1 * time.Second)

	// WHEN
	output = pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.05, output)
}

func TestPidLoop_I(t *testing.T) {
	// GIVEN
	p, i, d := 0.0, 0.01, 0.0
	pidLoop := NewPidLoop(p, i, d, 0, 255, true, true)

	// WHEN
	output := pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.0, output)

	time.Sleep(1 * time.Second)

	// WHEN
	output = pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.InDelta(t, 0.0, output, 0.01)
}

func TestPidLoop_D(t *testing.T) {
	// GIVEN
	p, i, d := 0.0, 0.0, 0.01
	pidLoop := NewPidLoop(p, i, d, 0, 255, true, true)

	// WHEN
	output := pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.0, output)

	time.Sleep(1 * time.Second)

	// WHEN
	output = pidLoop.Loop(10.0, 8.0)
	// THEN
	assert.InDelta(t, 0, output, 0.01)
}

// elapse moves the loop's clock back, as if `seconds` had passed since the last Loop call.
func elapse(pidLoop *PidLoop, seconds float64) {
	pidLoop.lastTime = pidLoop.lastTime.Add(-time.Duration(seconds * float64(time.Second)))
}

func TestPidLoop_CoolingGains_NoIntegralWindupWhileSaturatedAtMin(t *testing.T) {
	// GIVEN a cooling loop (negative gains) idling far below its setpoint for an hour
	pidLoop := NewPidLoop(-10, -0.5, 0, 0, 255, true, false) // as curves.NewSpeedCurve builds it
	pidLoop.Loop(80, 35)
	for n := 0; n < 3600; n++ {
		elapse(pidLoop, 1)
		assert.Equal(t, 0.0, pidLoop.Loop(80, 35))
	}

	// WHEN the temperature jumps above the setpoint
	elapse(pidLoop, 1)
	output := pidLoop.Loop(80, 90)

	// THEN the output responds at once instead of unwinding an hour of integral first
	assert.InDelta(t, 100.0, output, 0.01)
}

func TestPidLoop_CoolingGains_NoIntegralWindupWhileSaturatedAtMax(t *testing.T) {
	// GIVEN a cooling loop held at full output far above its setpoint for an hour
	pidLoop := NewPidLoop(-10, -0.5, 0, 0, 255, true, false) // as curves.NewSpeedCurve builds it
	pidLoop.Loop(80, 120)
	for n := 0; n < 3600; n++ {
		elapse(pidLoop, 1)
		assert.Equal(t, 255.0, pidLoop.Loop(80, 120))
	}

	// WHEN the temperature drops below the setpoint and stays there
	elapse(pidLoop, 1)
	output := pidLoop.Loop(80, 70)
	assert.Less(t, output, 255.0)
	for n := 0; n < 120; n++ {
		elapse(pidLoop, 1)
		output = pidLoop.Loop(80, 70)
	}

	// THEN the integral unwinds and the output reaches its minimum within two minutes
	assert.Equal(t, 0.0, output)
}

func TestPidLoop_PositiveGains_NoIntegralWindupWhileSaturatedAtMin(t *testing.T) {
	// GIVEN a loop with positive gains held at its minimum for an hour
	pidLoop := NewPidLoop(10, 0.5, 0, 0, 255, true, false) // as curves.NewSpeedCurve builds it
	pidLoop.Loop(35, 80)
	for n := 0; n < 3600; n++ {
		elapse(pidLoop, 1)
		assert.Equal(t, 0.0, pidLoop.Loop(35, 80))
	}

	// WHEN the error changes sign
	elapse(pidLoop, 1)
	output := pidLoop.Loop(90, 80)

	// THEN the output responds at once
	assert.InDelta(t, 100.0, output, 0.01)
}
