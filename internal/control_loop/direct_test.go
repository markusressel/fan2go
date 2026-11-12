package control_loop

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestSimple(t *testing.T) {
	// GIVEN
	loop := NewDirectControlLoop(nil)
	loop.Cycle(0)

	// WHEN
	newTarget := loop.Cycle(10)

	// THEN
	assert.Equal(t, 10.0, newTarget)

	// WHEN
	newTarget = loop.Cycle(10)

	// THEN
	assert.Equal(t, 10.0, newTarget)
}

func TestMaxChange(t *testing.T) {
	// GIVEN
	maxChangePerCycle := 2
	loop := NewDirectControlLoop(&maxChangePerCycle)
	loop.Cycle(0)

	// WHEN
	newTarget := loop.Cycle(10)

	// THEN
	assert.Equal(t, 2.0, newTarget)

	// WHEN
	newTarget = loop.Cycle(10)

	// THEN
	assert.Equal(t, 4.0, newTarget)
}

func TestDirect_OutOfRangeTargetIsClamped(t *testing.T) {
	// GIVEN
	loop := NewDirectControlLoop(nil)

	// WHEN
	first := loop.Cycle(300)
	second := loop.Cycle(300)

	// THEN the first cycle is clamped to [0..255] like all following cycles
	assert.Equal(t, 255.0, first)
	assert.Equal(t, 255.0, second)

	// GIVEN
	loop = NewDirectControlLoop(nil)

	// WHEN
	first = loop.Cycle(-10)
	second = loop.Cycle(-10)

	// THEN
	assert.Equal(t, 0.0, first)
	assert.Equal(t, 0.0, second)
}

func TestDirect_MaxChange_OutOfRangeTargetDoesNotShiftTheRampBase(t *testing.T) {
	// GIVEN
	maxChangePerCycle := 10
	loop := NewDirectControlLoop(&maxChangePerCycle)

	// WHEN the first targets are out of range, and then the target drops
	assert.Equal(t, 255.0, loop.Cycle(300))
	assert.Equal(t, 255.0, loop.Cycle(400))
	next := loop.Cycle(0)

	// THEN the ramp continues from the applied (clamped) value, not from the out-of-range target
	assert.Equal(t, 245.0, next)
}
