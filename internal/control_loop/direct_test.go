package control_loop

import (
	"time"

	"github.com/stretchr/testify/assert"
	"testing"
)

func TestSimple(t *testing.T) {
	// GIVEN
	loop := NewDirectControlLoop(nil)
	loop.Cycle(time.Time{}, 0)

	// WHEN
	newTarget := loop.Cycle(time.Time{}, 10)

	// THEN
	assert.Equal(t, 10.0, newTarget)

	// WHEN
	newTarget = loop.Cycle(time.Time{}, 10)

	// THEN
	assert.Equal(t, 10.0, newTarget)
}

func TestMaxChange(t *testing.T) {
	// GIVEN
	maxChangePerCycle := 2
	loop := NewDirectControlLoop(&maxChangePerCycle)
	loop.Cycle(time.Time{}, 0)

	// WHEN
	newTarget := loop.Cycle(time.Time{}, 10)

	// THEN
	assert.Equal(t, 2.0, newTarget)

	// WHEN
	newTarget = loop.Cycle(time.Time{}, 10)

	// THEN
	assert.Equal(t, 4.0, newTarget)
}

func TestDirect_OutOfRangeTargetIsClamped(t *testing.T) {
	// GIVEN
	loop := NewDirectControlLoop(nil)

	// WHEN
	first := loop.Cycle(time.Time{}, 300)
	second := loop.Cycle(time.Time{}, 300)

	// THEN the first cycle is clamped to [0..255] like all following cycles
	assert.Equal(t, 255.0, first)
	assert.Equal(t, 255.0, second)

	// GIVEN
	loop = NewDirectControlLoop(nil)

	// WHEN
	first = loop.Cycle(time.Time{}, -10)
	second = loop.Cycle(time.Time{}, -10)

	// THEN
	assert.Equal(t, 0.0, first)
	assert.Equal(t, 0.0, second)
}

func TestDirect_MaxChange_OutOfRangeTargetDoesNotShiftTheRampBase(t *testing.T) {
	// GIVEN
	maxChangePerCycle := 10
	loop := NewDirectControlLoop(&maxChangePerCycle)

	// WHEN the first targets are out of range, and then the target drops
	assert.Equal(t, 255.0, loop.Cycle(time.Time{}, 300))
	assert.Equal(t, 255.0, loop.Cycle(time.Time{}, 400))
	next := loop.Cycle(time.Time{}, 0)

	// THEN the ramp continues from the applied (clamped) value, not from the out-of-range target
	assert.Equal(t, 245.0, next)
}

func TestDirect_Preset_MaxChangeRampsFromThePresetValue(t *testing.T) {
	// GIVEN
	maxChangePerCycle := 10
	loop := NewDirectControlLoop(&maxChangePerCycle)
	assert.Equal(t, 100.0, loop.Cycle(time.Time{}, 100))

	// WHEN: the fan was set to max without the loop
	loop.Preset(255)

	// THEN: the ramp continues from max, not from the last output
	assert.Equal(t, 245.0, loop.Cycle(time.Time{}, 100))
	assert.Equal(t, 235.0, loop.Cycle(time.Time{}, 100))
}

func TestDirect_Preset_WithoutMaxChange(t *testing.T) {
	// GIVEN
	loop := NewDirectControlLoop(nil)
	loop.Cycle(time.Time{}, 100)

	// WHEN
	loop.Preset(255)

	// THEN
	assert.Equal(t, 100.0, loop.Cycle(time.Time{}, 100))
}

func TestDirect_PresetIsClamped(t *testing.T) {
	// GIVEN
	maxChangePerCycle := 10
	loop := NewDirectControlLoop(&maxChangePerCycle)

	// WHEN
	loop.Preset(300)

	// THEN
	assert.Equal(t, 245.0, loop.Cycle(time.Time{}, 0))
}

func TestDirect_Reset(t *testing.T) {
	// GIVEN
	maxChangePerCycle := 10
	loop := NewDirectControlLoop(&maxChangePerCycle)
	loop.Cycle(time.Time{}, 100)
	loop.Preset(255)

	// WHEN
	loop.Reset()

	// THEN: the next cycle applies its target directly, like the first cycle
	assert.Equal(t, 30.0, loop.Cycle(time.Time{}, 30))
	assert.Equal(t, 40.0, loop.Cycle(time.Time{}, 50))
}
