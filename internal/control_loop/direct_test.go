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
