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

func TestDirect_FirstCycleOutOfRangeTargetIsNotClamped(t *testing.T) {
	// Pins the current behaviour: the first cycle returns the target as is,
	// only the following cycles are clamped to [0..255].
	// GIVEN
	loop := NewDirectControlLoop(nil)

	// WHEN
	first := loop.Cycle(300)
	second := loop.Cycle(300)

	// THEN
	assert.Equal(t, 300.0, first)
	assert.Equal(t, 255.0, second)

	// GIVEN
	loop = NewDirectControlLoop(nil)

	// WHEN
	first = loop.Cycle(-10)
	second = loop.Cycle(-10)

	// THEN
	assert.Equal(t, -10.0, first)
	assert.Equal(t, 0.0, second)
}
