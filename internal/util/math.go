package util

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/ui"
	"sort"
	"strconv"
)

const (
	InterpolationTypeLinear = "linear"
)

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

// Ratio calculates the ration that target has in comparison to rangeMin and rangeMax
// Make sure that:
// rangeMin <= target <= rangeMax
// rangeMax - rangeMin != 0
func Ratio(target float64, rangeMin float64, rangeMax float64) float64 {
	return (target - rangeMin) / (rangeMax - rangeMin)
}

// UpdateSimpleMovingAvg calculates the new moving average, based on an existing average and buffer size
func UpdateSimpleMovingAvg(oldAvg float64, n int, newValue float64) float64 {
	return oldAvg + (1/float64(n))*(newValue-oldAvg)
}

func InterpolateLinearly(data *map[int]float64, start int, stop int) map[int]float64 {
	interpolated := map[int]float64{}
	for i := start; i <= stop; i++ {
		interpolatedValue := CalculateInterpolatedCurveValue(*data, InterpolationTypeLinear, float64(i))
		interpolated[i] = interpolatedValue
	}
	return interpolated
}

// CalculateInterpolatedCurveValue creates an interpolated function from the given map of x-values -> y-values
// as specified by the interpolationType and returns the y-value for the given input
func CalculateInterpolatedCurveValue(steps map[int]float64, interpolationType string, input float64) float64 {
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
			return steps[currentX]
		}

		if input >= float64(nextX) {
			continue
		}

		if input == float64(currentX) {
			return steps[currentX]
		} else {
			// input is somewhere in between currentX and nextX
			currentY := steps[currentX]
			nextY := steps[nextX]

			ratio := Ratio(input, float64(currentX), float64(nextX))
			interpolation := currentY + ratio*(nextY-currentY)
			return interpolation
		}
	}

	// input is above (or equal to) the largest given
	// step, so we fall back to the value of the largest step
	return steps[xValues[len(xValues)-1]]
}
