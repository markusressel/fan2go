package util

import "github.com/asecurityteam/rolling"

func CreateRollingWindow(size int) *rolling.PointPolicy {
	return rolling.NewPointPolicy(rolling.NewWindow(size))
}

// GetWindowAvg returns the average of all values in the window
func GetWindowAvg(window *rolling.PointPolicy) float64 {
	return window.Reduce(rolling.Avg)
}
