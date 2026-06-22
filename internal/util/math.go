package util

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/markusressel/fan2go/internal/ui"
)

const (
	InterpolationTypeLinear = "linear"
	InterpolationTypeStep   = "step"
)

type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64
}

// Coerce returns a value that is at least min and at most max, otherwise value
func Coerce[T Number](value T, min T, max T) T {
	if value > max {
		return max
	}
	if value < min {
		return min
	}
	return value
}

// Avg calculates the average of all values in the given array
func Avg(values []float64) float64 {
	sum := 0.0
	for i := 0; i < len(values); i++ {
		sum += values[i]
	}
	return sum / (float64(len(values)))
}

// HexString parses the given string as hex and string formats it,
// removing any leading zeros in the process
func HexString(hex string) string {
	value, err := strconv.ParseInt(hex, 16, 64)
	if err != nil {
		ui.Warning("Unable to parse value as hex: %s", hex)
		return hex
	}
	return fmt.Sprintf("%X", value)
}

// Ratio calculates the ratio that target has in comparison to rangeMin and rangeMax
// Make sure that:
// rangeMin <= target <= rangeMax
// rangeMax - rangeMin != 0
func Ratio(target float64, rangeMin float64, rangeMax float64) float64 {
	return ((target - rangeMin) / (rangeMax - rangeMin) * 100) / 100
}

// UpdateSimpleMovingAvg calculates the new moving average, based on an existing average and buffer size
func UpdateSimpleMovingAvg(oldAvg float64, n int, newValue float64) float64 {
	return oldAvg + (1/float64(n))*(newValue-oldAvg)
}

// ExpandMapToFullRange takes a map with key-value "control points" and expands it
// to a map with keys from minOutputKey to maxOutputKey (inclusive).
// Values for the output map are determined by segmenting the output key range
// based on the number of input control points.
// Precondition: inputControlPoints must not be empty.
// If minOutputKey > maxOutputKey, an empty map is returned.
func ExpandMapToFullRange(inputControlPoints map[int]int, minOutputKey int, maxOutputKey int) map[int]int {
	// Handle invalid output range: if min > max, return an empty map.
	if minOutputKey > maxOutputKey {
		return make(map[int]int)
	}

	// Extract keys from inputControlPoints and sort them for predictable value ordering.
	inputKeys := make([]int, 0, len(inputControlPoints))
	for k := range inputControlPoints {
		inputKeys = append(inputKeys, k)
	}
	sort.Ints(inputKeys)

	numInputEntries := len(inputKeys)

	// The input map cannot be empty, as output values must come from its valueset.
	if numInputEntries == 0 {
		panic("ExpandMapToFullRange: inputControlPoints cannot be empty.")
	}

	// Create a list of values from inputControlPoints, ordered by the sorted keys.
	sortedInputValues := make([]int, numInputEntries)
	for i, key := range inputKeys {
		sortedInputValues[i] = inputControlPoints[key]
	}

	// Calculate the size of the output key range.
	outputRangeSize := maxOutputKey - minOutputKey + 1

	// Initialize the output map with a capacity for the output range size.
	outputMap := make(map[int]int, outputRangeSize)

	// Calculate breakpoints that define the start of each segment within the output key range.
	// actualBreakpoints[k] is the 0-indexed start of the k-th segment relative to minOutputKey.
	actualBreakpoints := make([]int, numInputEntries+1)
	for k := 0; k < numInputEntries; k++ {
		// The k-th segment (0-indexed) starts at floor(k * outputRangeSize / numInputEntries).
		actualBreakpoints[k] = int(math.Floor((float64(k) * float64(outputRangeSize)) / float64(numInputEntries)))
	}
	// This sentinel marks the exclusive end of the range for the last value (i.e., total size of the range).
	actualBreakpoints[numInputEntries] = outputRangeSize

	// Populate the outputMap by assigning values to segments.
	currentValueIndex := 0 // Index for sortedInputValues.
	for i := 0; i < outputRangeSize; i++ {
		outKey := minOutputKey + i // Calculate the actual key for the output map.

		// If the current position `i` has reached or crossed the start of the *next* segment, move to the next value.
		// The check `currentValueIndex < numInputEntries-1` ensures there is a next segment.
		if currentValueIndex < numInputEntries-1 {
			if i >= actualBreakpoints[currentValueIndex+1] {
				currentValueIndex++
			}
		}
		outputMap[outKey] = sortedInputValues[currentValueIndex]
	}

	return outputMap
}

// InterpolateStepInt integer specific variant of InterpolateStep.
func InterpolateStepInt(data *map[int]int, start int, stop int) (map[int]int, error) {
	floatData := map[int]float64{}
	for k, v := range *data {
		floatData[k] = float64(v)
	}
	interpolatedFloat, err := InterpolateStep(&floatData, start, stop)
	if err != nil {
		return map[int]int{}, fmt.Errorf("error interpolating flat: %w", err)
	}
	interpolated := map[int]int{}
	for k, v := range interpolatedFloat {
		interpolated[k] = int(v)
	}
	return interpolated, nil
}

// InterpolateLinearlyInt integer specific variant of InterpolateLinearly.
func InterpolateLinearlyInt(data *map[int]int, start int, stop int) (map[int]int, error) {
	floatData := map[int]float64{}
	for k, v := range *data {
		floatData[k] = float64(v)
	}
	interpolatedFloat, err := InterpolateLinearly(&floatData, start, stop)
	if err != nil {
		return map[int]int{}, fmt.Errorf("error interpolating linearly: %w", err)
	}
	interpolated := map[int]int{}
	for k, v := range interpolatedFloat {
		interpolated[k] = int(v)
	}
	return interpolated, nil
}

// InterpolateStep takes the given mapping and adds flat values in [start;stop].
func InterpolateStep(data *map[int]float64, start int, stop int) (map[int]float64, error) {
	interpolated := map[int]float64{}
	// copy existing values
	for k, v := range *data {
		interpolated[k] = v
	}

	for i := start; i <= stop; i++ {
		interpolatedValue, err := CalculateInterpolatedCurveValue(*data, InterpolationTypeStep, float64(i))
		if err != nil {
			return interpolated, fmt.Errorf("error calculating interpolated value for %d: %w", i, err)
		}
		interpolated[i] = interpolatedValue
	}
	return interpolated, nil
}

// InterpolateLinearly takes the given mapping and adds interpolated values in [start;stop].
func InterpolateLinearly(data *map[int]float64, start int, stop int) (map[int]float64, error) {
	interpolated := map[int]float64{}
	// copy existing values
	for k, v := range *data {
		interpolated[k] = v
	}

	for i := start; i <= stop; i++ {
		interpolatedValue, err := CalculateInterpolatedCurveValue(*data, InterpolationTypeLinear, float64(i))
		if err != nil {
			return interpolated, fmt.Errorf("error calculating interpolated value for %d: %w", i, err)
		}
		interpolated[i] = interpolatedValue
	}
	return interpolated, nil
}

// CalculateInterpolatedCurveValue creates an interpolated function from the given map of x-values -> y-values
// as specified by the interpolationType and returns the y-value for the given input
func CalculateInterpolatedCurveValue(steps map[int]float64, interpolationType string, input float64) (float64, error) {
	xValues := make([]int, 0, len(steps))
	for x := range steps {
		xValues = append(xValues, x)
	}
	// sort them increasing
	sort.Ints(xValues)

	// find value closest to input
	for i := 0; i < len(xValues)-1; i++ {
		currentX := xValues[i]
		nextX := xValues[i+1]

		if input <= float64(currentX) && i == 0 {
			// input is below the smallest given step, so
			// we fall back to the value of the smallest step
			return steps[currentX], nil
		}

		if input >= float64(nextX) {
			continue
		}

		if input == float64(currentX) {
			return steps[currentX], nil
		} else {
			// input is somewhere in between currentX and nextX
			currentY := steps[currentX]
			nextY := steps[nextX]

			switch interpolationType {
			case InterpolationTypeLinear:
				ratio := Ratio(input, float64(currentX), float64(nextX))
				interpolation := currentY + (ratio * (nextY - currentY))
				const epsilonSnapping = 1e-8
				roundedInterpolation := math.Round(interpolation)
				if math.Abs(interpolation-roundedInterpolation) < epsilonSnapping {
					interpolation = roundedInterpolation
				}
				return interpolation, nil
			case InterpolationTypeStep:
				return currentY, nil
			default:
				return 0.0, fmt.Errorf("unknown interpolation type: %s", interpolationType)
			}
		}
	}

	// input is above (or equal to) the largest given
	// step, so we fall back to the value of the largest step
	return steps[xValues[len(xValues)-1]], nil
}

// EnsureMonotonicallyIncreasing takes a map of int to float and ensures that the values are monotonically increasing with increasing keys.
// If a value is found to be smaller than the previous value, it is replaced by the previous value.
// Note that this method DOES NOT guarantee strict monotonicity, but only non-decreasing values.
// It also DOES NOT guarantee monotonicity outside the given [start;stop] range.
func EnsureMonotonicallyIncreasing(data map[int]float64, start int, stop int) map[int]float64 {
	monotonic := map[int]float64{}
	// copy existing values
	for id := range data {
		monotonic[id] = data[id]
	}

	keysInRange := make([]int, 0, len(data))
	for k := range data {
		if k >= start && k <= stop {
			keysInRange = append(keysInRange, k)
		}
	}
	sort.Ints(keysInRange)

	hasLast := false
	lastValue := 0.0
	for _, k := range keysInRange {
		value := data[k]
		if !hasLast {
			monotonic[k] = value
			lastValue = value
			hasLast = true
			continue
		}
		if value < lastValue {
			monotonic[k] = lastValue
			continue
		}
		monotonic[k] = value
		lastValue = value
	}
	return monotonic
}

// FindClosest finds the closest value to target in options.
// Assumes that arr is sorted in ascending order.
func FindClosest(target int, arr []int) int {
	n := len(arr)

	// Corner cases
	if target <= arr[0] {
		return arr[0]
	}
	if target >= arr[n-1] {
		return arr[n-1]
	}

	i := 0
	j := len(arr)
	mid := 0

	for i < j {
		mid = (i + j) / 2

		if arr[mid] == target {
			break
		}

		/* If target is less than array element,
		   then search in left */
		if target < arr[mid] {
			// If target is greater than previous
			// to mid, return closest of two
			if mid > 0 && target > arr[mid-1] {
				return getClosest(arr[mid-1], arr[mid], target)
			}

			/* Repeat for left half */
			j = mid
		} else {
			// If target is greater than mid

			if mid < n-1 && target < arr[mid+1] {
				return getClosest(arr[mid], arr[mid+1], target)
			}
			// update i
			i = mid + 1
		}
	}

	// Only single element left after search
	return arr[mid]
}

// Returns the value that is closer to target.
// Assumes that val1 < target < val2.
// If the distance of val1 to target is equal to the distance of val2 to target,
// the smaller value is returned.
func getClosest(val1 int, val2 int, target int) int {
	if target-val1 > val2-target { // If val1 is strictly further away than val2
		return val2 // Then val2 is closer
	} else { // Otherwise, val1 is closer or they are equidistant
		return val1 // Return val1 (it's closer or it's a tie and val1 is smaller)
	}
}

const (
	UintMax = ^uint(0)
	IntMax  = int(UintMax >> 1)
	IntMin  = -IntMax - 1
)

// Returns absolute value of given int
// returns slightly wrong result for IntMin, because -IntMin can't be represented as signed int
func Abs(val int) int {
	if val >= 0 {
		return val
	} else if val == IntMin {
		// -IntMin can't be represented as int, so it wraps around and remains IntMin.
		// Return IntMax instead, that's at least positive and close to the real value
		// (but is really -(IntMin + 1) not -IntMin)
		return IntMax
	}
	return -val
}

// returns absolute value of given int, as uint
// (so it works even for IntMin)
func AbsU(val int) uint {
	if val >= 0 {
		return uint(val)
	}
	// yes, -uint(val) and NOT uint(-val).
	// this works even with IntMin, the other does not. an explanation can be found at:
	// https://graphics.stanford.edu/~seander/bithacks.html#IntegerAbs
	return -uint(val)
}

func MinValOrElse(values []float64, defaultVal float64) float64 {
	if len(values) == 0 {
		return defaultVal
	}
	minVal := values[0]
	for _, v := range values {
		minVal = math.Min(minVal, v)
	}
	return minVal
}

func MaxValOrElse(values []float64, defaultVal float64) float64 {
	if len(values) == 0 {
		return defaultVal
	}
	maxVal := values[0]
	for _, v := range values {
		maxVal = math.Max(maxVal, v)
	}
	return maxVal
}

// IsMonotonicallyIncreasing validates that map values are monotonically increasing when iterating keys in ascending order.
func IsMonotonicallyIncreasing(values map[int]int) error {
	return validateMonotonicallyIncreasing(values, false)
}

// IsStrictlyMonotonicallyIncreasing validates that map values are strictly monotonically increasing when iterating keys in ascending order.
func IsStrictlyMonotonicallyIncreasing(values map[int]int) error {
	return validateMonotonicallyIncreasing(values, true)
}

func validateMonotonicallyIncreasing(values map[int]int, strict bool) error {
	sortedKeys := SortedKeys(values)
	for i := 1; i < len(sortedKeys); i++ {
		prevKey := sortedKeys[i-1]
		currKey := sortedKeys[i]
		prevVal := values[prevKey]
		currVal := values[currKey]

		if strict && currVal <= prevVal {
			return fmt.Errorf("values must be strictly monotonically increasing (at keys %d and %d: %d <= %d)", prevKey, currKey, currVal, prevVal)
		}
		if !strict && currVal < prevVal {
			return fmt.Errorf("values must be monotonically increasing (at keys %d and %d: %d < %d)", prevKey, currKey, currVal, prevVal)
		}
	}

	return nil
}

// MedianFloat64 returns the median of a non-empty slice of float64 values.
func MedianFloat64(values []float64) float64 {
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 0 {
		return (sorted[n/2-1] + sorted[n/2]) / 2.0
	}
	return sorted[n/2]
}

func Sum(values []float64) float64 {
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum
}

func Difference(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	diff := values[0]
	for i := 1; i < len(values); i++ {
		diff -= values[i]
	}
	return diff
}

func Delta(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	min := MinValOrElse(values, values[0])
	max := MaxValOrElse(values, values[0])
	return max - min
}
