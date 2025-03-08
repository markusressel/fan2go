package control_loop

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestSimple(t *testing.T) {
	// GIVEN
	loop := NewDirectControlLoop(nil)

	// WHEN
	newTarget := loop.Cycle(10, 0)

	// THEN
	assert.Equal(t, 10, newTarget)

	// WHEN
	newTarget = loop.Cycle(10, newTarget)

	// THEN
	assert.Equal(t, 10, newTarget)
}

func TestMaxChange(t *testing.T) {
	// GIVEN
	maxChangePerCycle := 2
	loop := NewDirectControlLoop(&maxChangePerCycle)

	// WHEN
	newTarget := loop.Cycle(10, 0)

	// THEN
	assert.Equal(t, 2, newTarget)

	// WHEN
	newTarget = loop.Cycle(10, newTarget)

	// THEN
	assert.Equal(t, 4, newTarget)
}
