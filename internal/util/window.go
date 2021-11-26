package util

import "github.com/asecurityteam/rolling"

func CreateRollingWindow(size int) *rolling.PointPolicy {
	return rolling.NewPointPolicy(rolling.NewWindow(size))
}
