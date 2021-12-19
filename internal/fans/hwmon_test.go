package fans

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestHwMonFan_GetStartPwm(t *testing.T) {
	// GIVEN
	expected := 30
	fan := HwMonFan{
		StartPwm: &expected,
	}

	// WHEN
	startPwm := fan.GetStartPwm()

	// THEN
	assert.Equal(t, expected, startPwm)
}

func TestHwMonFan_SetStartPwm(t *testing.T) {
	// GIVEN
	expected := 30
	fan := HwMonFan{}

	// WHEN
	fan.SetStartPwm(expected)
	startPwm := fan.GetStartPwm()

	// THEN
	assert.Equal(t, expected, startPwm)
}

func TestHwMonFan_ShouldNeverStop_GetMinPwm(t *testing.T) {
	// GIVEN
	expected := 30
	fan := HwMonFan{
		Config: configuration.FanConfig{
			NeverStop: true,
		},
		MinPwm: expected,
	}

	// WHEN
	minPwm := fan.GetMinPwm()

	// THEN
	assert.Equal(t, expected, minPwm)
}

func TestHwMonFan_GetMinPwm(t *testing.T) {
	// GIVEN
	expected := 0
	fan := HwMonFan{
		Config: configuration.FanConfig{
			NeverStop: false,
		},
		MinPwm: 30,
	}

	// WHEN
	minPwm := fan.GetMinPwm()

	// THEN
	assert.Equal(t, expected, minPwm)
}

func TestHwMonFan_GetMaxPwm(t *testing.T) {
	// GIVEN
	expected := 240
	fan := HwMonFan{
		MaxPwm: expected,
	}

	// WHEN
	maxPwm := fan.GetMaxPwm()

	// THEN
	assert.Equal(t, expected, maxPwm)
}

func TestHwMonFan_SetMaxPwm(t *testing.T) {
	// GIVEN
	expected := 240
	fan := HwMonFan{}

	// WHEN
	fan.SetMaxPwm(expected)
	maxPwm := fan.GetMaxPwm()

	// THEN
	assert.Equal(t, expected, maxPwm)
}
