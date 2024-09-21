package util

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestGetWindowMax(t *testing.T) {
	// GIVEN
	window := CreateRollingWindow(3)
	window.Append(1)
	window.Append(2)
	window.Append(3)

	// WHEN
	maximumm := GetWindowMax(window)

	// THEN
	assert.Equal(t, 3.0, maximumm)
}
