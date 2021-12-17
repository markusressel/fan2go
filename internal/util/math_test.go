package util

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestCalculateInterpolatedCurveValue(t *testing.T) {
	// GIVEN
	expectedInputOutput := map[float64]float64{
		//-100.0: 0.0,
		0:      0.0,
		100.0:  100.0,
		500.0:  500.0,
		1000.0: 1000.0,
		2000.0: 1000.0,
	}
	steps := map[int]float64{
		0:    0,
		100:  100,
		1000: 1000,
	}
	interpolationType := InterpolationTypeLinear

	for input, output := range expectedInputOutput {
		// WHEN
		result := CalculateInterpolatedCurveValue(steps, interpolationType, input)

		// THEN
		assert.Equal(t, output, result)
	}
}

func TestRatio(t *testing.T) {
	// GIVEN
	a := 0.0
	b := 100.0
	c := 50.0

	expected := 0.5

	// WHEN
	result := Ratio(c, a, b)

	// THEN
	assert.Equal(t, expected, result)
}

func TestFindClosest(t *testing.T) {
	// GIVEN
	options := []int{
		10, 20, 30, 40, 50, 60, 70, 80, 90,
	}

	// WHEN
	closest := FindClosest(5, options)
	// THEN
	assert.Equal(t, 10, closest)

	// WHEN
	closest = FindClosest(54, options)
	// THEN
	assert.Equal(t, 50, closest)

	// WHEN
	closest = FindClosest(55, options)

	// THEN
	assert.Equal(t, 60, closest)

	// WHEN
	closest = FindClosest(100, options)
	// THEN
	assert.Equal(t, 90, closest)

}
