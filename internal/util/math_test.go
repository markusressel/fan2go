package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
		result, err := CalculateInterpolatedCurveValue(steps, interpolationType, input)

		// THEN
		assert.NoError(t, err)
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
	closest = FindClosest(11, options)
	// THEN
	assert.Equal(t, 10, closest)

	// WHEN
	closest = FindClosest(50, options)
	// THEN
	assert.Equal(t, 50, closest)

	// WHEN
	closest = FindClosest(54, options)
	// THEN
	assert.Equal(t, 50, closest)

	// WHEN
	closest = FindClosest(55, options)
	// THEN
	assert.Equal(t, 50, closest)

	// WHEN
	closest = FindClosest(75, options)
	// THEN
	assert.Equal(t, 70, closest)

	// WHEN
	closest = FindClosest(100, options)
	// THEN
	assert.Equal(t, 90, closest)

}

func TestCoerce(t *testing.T) {
	// GIVEN
	min := 0.0
	max := 10.0

	// WHEN
	resultMin := Coerce(-10, min, max)
	// THEN
	assert.Equal(t, min, resultMin)

	// WHEN
	resultValueLow := Coerce(0, min, max)
	// THEN
	assert.Equal(t, resultValueLow, resultValueLow)

	// WHEN
	resultValueHigh := Coerce(10, min, max)

	// THEN
	assert.Equal(t, resultValueHigh, resultValueHigh)

	// WHEN
	resultMax := Coerce(20, min, max)
	// THEN
	assert.Equal(t, max, resultMax)
}

func TestUpdateSimpleMovingAvg(t *testing.T) {
	// GIVEN
	avg := 0.0
	n := 2
	newValue := 10.0

	// WHEN
	result := UpdateSimpleMovingAvg(avg, n, newValue)

	// THEN
	assert.Equal(t, 5.0, result)
}

func TestInterpolateLinearly(t *testing.T) {
	// GIVEN
	data := map[int]float64{
		0:   0.0,
		100: 100.0,
	}
	start := 0
	stop := 100

	expectedResult := map[int]float64{}
	for i := 0; i <= 100; i++ {
		expectedResult[i] = float64(i)
	}

	// WHEN
	result, err := InterpolateLinearly(&data, start, stop)

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, expectedResult, result)
}

func TestGetClosest(t *testing.T) {
	// GIVEN
	expectedResult := -1

	// WHEN
	result := getClosest(-1, 1, 0)

	// THEN
	assert.Equal(t, expectedResult, result)
}

func TestExpandMapToFullRange(t *testing.T) {
	// GIVEN
	data := map[int]int{0: 0, 128: 128, 255: 255}

	// WHEN
	result := ExpandMapToFullRange(data, 0, 255)

	// THEN
	expectedResult, err := InterpolateStepInt(
		&map[int]int{
			0:   0,
			85:  128,
			170: 255,
		},
		0,
		255,
	)
	assert.NoError(t, err)
	assert.Equal(t, expectedResult, result)
}

func TestExpandMapToFullRangeThreeValues(t *testing.T) {
	// GIVEN
	data := map[int]int{0: 0, 1: 1, 2: 2, 3: 3}

	// WHEN
	result := ExpandMapToFullRange(data, 0, 255)

	// THEN
	expectedResult, err := InterpolateStepInt(
		&map[int]int{
			0:   0,
			64:  1,
			128: 2,
			192: 3,
		},
		0,
		255,
	)
	assert.NoError(t, err)
	assert.Equal(t, expectedResult, result)
}

func TestExpandMapToFullRangeSingleValue(t *testing.T) {
	// GIVEN
	data := map[int]int{100: 100}

	// WHEN
	result := ExpandMapToFullRange(data, 0, 255)

	// THEN
	expectedResult, err := InterpolateStepInt(
		&map[int]int{
			100: 100,
		},
		0,
		255,
	)
	assert.NoError(t, err)
	assert.Equal(t, expectedResult, result)
}

func TestMinValOrElse(t *testing.T) {
	// GIVEN
	values := []float64{3.0, 2.0, 1.0}
	defaultValue := 0.0

	// WHEN
	result := MinValOrElse(values, defaultValue)

	// THEN
	assert.Equal(t, 1.0, result)
}

func TestMinValOrElseEmpty(t *testing.T) {
	// GIVEN
	values := []float64{}
	defaultValue := 10.0

	// WHEN
	result := MinValOrElse(values, defaultValue)

	// THEN
	assert.Equal(t, defaultValue, result)
}

func TestMaxValOrElse(t *testing.T) {
	// GIVEN
	values := []float64{1.0, 2.0, 3.0}
	defaultValue := 0.0

	// WHEN
	result := MaxValOrElse(values, defaultValue)

	// THEN
	assert.Equal(t, 3.0, result)
}

func TestMaxValOrElseEmpty(t *testing.T) {
	// GIVEN
	values := []float64{}
	defaultValue := 10.0

	// WHEN
	result := MaxValOrElse(values, defaultValue)

	// THEN
	assert.Equal(t, defaultValue, result)
}
