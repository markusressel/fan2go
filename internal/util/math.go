package util

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/ui"
	"strconv"
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
