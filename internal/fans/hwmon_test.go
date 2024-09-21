package fans

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"os"
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

func TestHwMonFan_GetStartPwm_Default(t *testing.T) {
	// GIVEN
	expected := MaxPwmValue
	fan := HwMonFan{}

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

func TestHwMonFan_GetMaxPwm_Default(t *testing.T) {
	// GIVEN
	expected := MaxPwmValue
	fan := HwMonFan{
		MaxPwm: &expected,
		Config: configuration.FanConfig{},
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

func TestHwMonFan_GetRpm(t *testing.T) {
	// GIVEN
	expected := 2150
	fan := HwMonFan{
		Config: configuration.FanConfig{
			HwMon: &configuration.HwMonFanConfig{
				RpmInputPath: "../../test/file_fan_rpm",
			},
		},
	}

	// WHEN
	rpm, err := fan.GetRpm()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, expected, fan.Rpm)
	assert.Equal(t, expected, rpm)
}

func TestHwMonFan_GetRpmAvg(t *testing.T) {
	// GIVEN
	expected := 2150.0
	fan := HwMonFan{
		RpmMovingAvg: expected,
	}

	// WHEN
	rpm := fan.GetRpmAvg()

	// THEN
	assert.Equal(t, expected, rpm)
}

func TestHwMonFan_SetRpmAvg(t *testing.T) {
	// GIVEN
	expected := 2150.0
	fan := HwMonFan{}

	// WHEN
	fan.SetRpmAvg(expected)
	rpm := fan.GetRpmAvg()

	// THEN
	assert.Equal(t, expected, rpm)
}

func TestHwMonFan_GetPwm(t *testing.T) {
	// GIVEN
	expected := 152
	fan := HwMonFan{
		Config: configuration.FanConfig{
			HwMon: &configuration.HwMonFanConfig{
				PwmPath: "../../test/file_fan_pwm",
			},
		},
	}

	// WHEN
	pwm, err := fan.GetPwm()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, expected, pwm)
}

func TestHwMonFan_SetPwm(t *testing.T) {
	// GIVEN
	pwmFilePath := "./file_fan_pwm"
	defer func(name string) {
		_ = os.Remove(name)
	}(pwmFilePath)

	expected := 152
	fan := HwMonFan{
		Config: configuration.FanConfig{
			HwMon: &configuration.HwMonFanConfig{
				PwmPath: "../../test/file_fan_pwm",
			},
		},
	}

	// WHEN
	err := fan.SetPwm(expected)

	// THEN
	assert.NoError(t, err)

	result, err := fan.GetPwm()
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestHwMonFan_AttachFanCurveData(t *testing.T) {
	// GIVEN
	curveData := map[int]float64{
		0:   0,
		255: 255,
	}
	interpolated := util.InterpolateLinearly(&curveData, 10, 200)

	fan := HwMonFan{
		Config: configuration.FanConfig{
			NeverStop: true,
		},
	}

	// WHEN
	err := fan.AttachFanCurveData(&interpolated)

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, &interpolated, fan.GetFanCurveData())
	assert.Equal(t, 10, fan.GetMinPwm())
	assert.Equal(t, 10, fan.GetStartPwm())
	assert.Equal(t, 200, fan.GetMaxPwm())
}
