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
