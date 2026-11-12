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

func TestUpdateExponentialMovingAvg(t *testing.T) {
	// GIVEN
	avg := 0.0
	n := 2
	newValue := 10.0

	// WHEN
	result := UpdateExponentialMovingAvg(avg, n, newValue)

	// THEN
	assert.Equal(t, 5.0, result)
}

// samplesUntilEqual feeds target into the average until it equals target exactly
// and returns the number of samples needed, or -1 if it didn't within maxSamples.
func samplesUntilEqual(avg float64, n int, target float64, maxSamples int) int {
	for i := 1; i <= maxSamples; i++ {
		avg = UpdateExponentialMovingAvg(avg, n, target)
		if avg == target {
			return i
		}
	}
	return -1
}

func TestUpdateExponentialMovingAvg_ReachesTargetExactly(t *testing.T) {
	tests := []struct {
		name     string
		start    float64
		target   float64
		expected int
	}{
		{"rising", 59000, 60000, 73},
		{"rising, large step", 40000, 75000, 106},
		{"falling", 1000, 0, 73},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, samplesUntilEqual(tt.start, 10, tt.target, 100_000))
		})
	}
}

func TestUpdateExponentialMovingAvg_UnchangedWhileDiffIsAtLeastHalf(t *testing.T) {
	tests := []struct {
		oldAvg   float64
		newValue float64
		expected float64
	}{
		{0, 1000, 100},
		{1000, 0, 900},
		{59994, 60000, 59994.6}, // remaining diff 5.4
		{5, 0, 4.5},             // remaining diff 4.5
		{-5, 0, -4.5},           // remaining diff 4.5
		{0.6, 0, 0.54},          // remaining diff 0.54
	}
	for _, tt := range tests {
		assert.InDelta(t, tt.expected, UpdateExponentialMovingAvg(tt.oldAvg, 10, tt.newValue), 1e-9)
		assert.NotEqual(t, tt.newValue, UpdateExponentialMovingAvg(tt.oldAvg, 10, tt.newValue))
	}

	// a remaining diff of exactly 0.5 isn't snapped
	assert.Equal(t, 0.5, UpdateExponentialMovingAvg(1, 2, 0))
}

func TestUpdateExponentialMovingAvg_SnapsToTargetBelowHalf(t *testing.T) {
	// remaining diff 0.45 -> snapped
	assert.Equal(t, 0.0, UpdateExponentialMovingAvg(0.5, 10, 0))
	// remaining diff 0.45 -> snapped
	assert.Equal(t, 60000.0, UpdateExponentialMovingAvg(59999.5, 10, 60000))
	// remaining diff 0.45 -> snapped
	assert.Equal(t, -10.0, UpdateExponentialMovingAvg(-10.5, 10, -10))
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

func TestIsMonotonicallyIncreasing_AllowsEqualAdjacentValues(t *testing.T) {
	err := IsMonotonicallyIncreasing(map[int]int{0: 10, 64: 10, 255: 20})
	assert.NoError(t, err)
}

func TestIsMonotonicallyIncreasing_RejectsDecreasingValues(t *testing.T) {
	err := IsMonotonicallyIncreasing(map[int]int{0: 10, 64: 9, 255: 20})
	assert.EqualError(t, err, "values must be monotonically increasing (at keys 0 and 64: 9 < 10)")
}

func TestIsStrictlyMonotonicallyIncreasing_RejectsEqualAdjacentValues(t *testing.T) {
	err := IsStrictlyMonotonicallyIncreasing(map[int]int{0: 10, 64: 10, 255: 20})
	assert.EqualError(t, err, "values must be strictly monotonically increasing (at keys 0 and 64: 10 <= 10)")
}

func TestIsStrictlyMonotonicallyIncreasing_AllowsStrictIncrease(t *testing.T) {
	err := IsStrictlyMonotonicallyIncreasing(map[int]int{0: 10, 64: 11, 255: 20})
	assert.NoError(t, err)
}

func TestEnsureMonotonicallyIncreasing_SparseKeysInRange(t *testing.T) {
	data := map[int]float64{
		10:  5,
		20:  7,
		40:  100,
		80:  50,
		120: 75,
		200: 10,
	}

	result := EnsureMonotonicallyIncreasing(data, 40, 120)

	assert.Equal(t, 100.0, result[40])
	assert.Equal(t, 100.0, result[80])
	assert.Equal(t, 100.0, result[120])
	// Keys outside [start..stop] remain untouched.
	assert.Equal(t, 5.0, result[10])
	assert.Equal(t, 7.0, result[20])
	assert.Equal(t, 10.0, result[200])
}

func TestEnsureMonotonicallyIncreasing_DenseKeysInRange(t *testing.T) {
	data := map[int]float64{
		0: 10,
		1: 8,
		2: 9,
		3: 7,
	}

	result := EnsureMonotonicallyIncreasing(data, 0, 3)

	assert.Equal(t, 10.0, result[0])
	assert.Equal(t, 10.0, result[1])
	assert.Equal(t, 10.0, result[2])
	assert.Equal(t, 10.0, result[3])
}
