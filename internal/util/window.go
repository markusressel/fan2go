package util

import "github.com/asecurityteam/rolling"

func CreateRollingWindow(size int) *rolling.PointPolicy {
	return rolling.NewPointPolicy(rolling.NewWindow(size))
}

// GetWindowAvg returns the average of all values in the window
func GetWindowAvg(window *rolling.PointPolicy) float64 {
	return window.Reduce(rolling.Avg)
}

// FillWindow completely fills the given window with the given value
func FillWindow(window *rolling.PointPolicy, size int, value float64) {
	for i := 0; i < size; i++ {
		window.Append(value)
	}
}

// GetWindowMax returns the max value in the window
func GetWindowMax(window *rolling.PointPolicy) float64 {
	return window.Reduce(rolling.Max)
}
