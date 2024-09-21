package fans

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestHwMonFan_GetId(t *testing.T) {
	// GIVEN
	id := "test"
	config := configuration.FanConfig{
		ID:    id,
		HwMon: &configuration.HwMonFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.GetId()

	assert.Equal(t, id, result)
}

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
	fan.SetStartPwm(expected, false)
	startPwm := fan.GetStartPwm()

	// THEN
	assert.Equal(t, expected, startPwm)
}

func TestHwMonFan_ShouldNeverStop_GetMinPwm(t *testing.T) {
	// GIVEN
	expected := 30
	fan := HwMonFan{
		MinPwm: &expected,
		Config: configuration.FanConfig{
			NeverStop: true,
			MinPwm:    &expected,
			HwMon:     &configuration.HwMonFanConfig{},
		},
	}

	// WHEN
	minPwm := fan.GetMinPwm()

	// THEN
	assert.Equal(t, expected, minPwm)
}

func TestHwMonFan_GetMinPwm(t *testing.T) {
	// GIVEN
	expected := 0
	minPwm := 30
	fan := HwMonFan{
		Config: configuration.FanConfig{
			NeverStop: false,
			MinPwm:    &minPwm,
		},
	}

	// WHEN
	result := fan.GetMinPwm()

	// THEN
	assert.Equal(t, expected, result)
}

func TestHwMonFan_SetMinPwm(t *testing.T) {
	// GIVEN
	expected := 0
	minPwm := 30
	fan := HwMonFan{
		Config: configuration.FanConfig{
			NeverStop: false,
			MinPwm:    &minPwm,
		},
	}

	// WHEN
	fan.SetMinPwm(expected, true)

	// THEN
	result := fan.GetMinPwm()
	assert.Equal(t, expected, result)
}

func TestHwMonFan_GetMaxPwm(t *testing.T) {
	// GIVEN
	expected := 240
	fan := HwMonFan{
		MaxPwm: &expected,
		Config: configuration.FanConfig{
			MaxPwm: &expected,
		},
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
	fan.SetMaxPwm(expected, false)
	maxPwm := fan.GetMaxPwm()

	// THEN
	assert.Equal(t, expected, maxPwm)
}
