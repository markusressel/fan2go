package fans

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func TestFileFan_NewFan(t *testing.T) {
	// GIVEN
	id := "test"
	config := configuration.FanConfig{
		ID: id,
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	// WHEN
	fan, err := NewFan(config)

	// THEN
	assert.NoError(t, err)
	assert.NotNil(t, fan)
}

func TestFileFan_GetId(t *testing.T) {
	// GIVEN
	id := "test"
	config := configuration.FanConfig{
		ID: id,
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.GetId()

	assert.Equal(t, id, result)
}

func TestFileFan_GetStartPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.GetStartPwm()

	// THEN
	assert.Equal(t, 1, result)
}

func TestFileFan_SetStartPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	fan.SetStartPwm(100, false)

	// THEN
	// NOTE: file fan does not support setting start pwm
	assert.Equal(t, 1, fan.GetStartPwm())
}

func TestFileFan_GetMinPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result := fan.GetMinPwm()

	// THEN
	assert.Equal(t, 0, result)
}

func TestFileFan_SetMinPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	fan.SetMinPwm(100, false)

	// THEN
	// NOTE: file fan does not support setting start pwm
	assert.Equal(t, 0, fan.GetMinPwm())
}

func TestFileFan_GetMaxPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result := fan.GetMaxPwm()

	// THEN
	assert.Equal(t, 255, result)
}

func TestFileFan_SetMaxPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	fan.SetMaxPwm(100, false)

	// THEN
	// NOTE: file fan does not support setting max pwm
	assert.Equal(t, 255, fan.GetMaxPwm())
}

func TestFileFan_GetRpm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetRpm()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 2150, result)
}

func TestFileFan_GetRpm_InvalidPath(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/non_existent_file",
			RpmPath: "../../test/non_existent_file",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetRpm()

	// THEN
	assert.Error(t, err)
	assert.Equal(t, 0, result)
}

func TestFileFan_SetRpmAvg(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/non_existent_file",
			RpmPath: "../../test/non_existent_file",
		},
	}

	fan, _ := NewFan(config)

	rpmAvg := 1000.5

	// WHEN
	fan.SetRpmAvg(rpmAvg)

	// THEN
	assert.Equal(t, float64(int(rpmAvg)), fan.GetRpmAvg())
}

func TestFileFan_GetRpmAvg(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/non_existent_file",
			RpmPath: "../../test/non_existent_file",
		},
	}

	fan, _ := NewFan(config)

	rpmAvg := 1234.5
	fan.SetRpmAvg(rpmAvg)

	// WHEN
	result := fan.GetRpmAvg()

	// THEN
	assert.Equal(t, float64(int(rpmAvg)), result)
}

func TestFileFan_GetPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetPwm()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 152, result)
}

func TestFileFan_GetPwm_InvalidPath(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/non_existent_file",
			RpmPath: "../../test/non_existent_file",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetPwm()

	// THEN
	assert.Error(t, err)
	assert.Equal(t, 0, result)
}

func TestFileFan_SetPwm(t *testing.T) {
	// GIVEN
	defer os.Remove("./file_fan_pwm")

	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "./file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)
	targetPwm := 100

	// WHEN
	err := fan.SetPwm(targetPwm)

	// THEN
	assert.NoError(t, err)

	result, err := fan.GetPwm()

	assert.NoError(t, err)
	assert.Equal(t, targetPwm, result)
}

func TestFileFan_SetPwm_InvalidPath(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../..////",
			RpmPath: "../../test/non_existent_file",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	err := fan.SetPwm(100)

	// THEN
	assert.Error(t, err)
}

func TestFileFan_GetFanCurveData(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	expectedFanCurve := util.InterpolateLinearly(
		&map[int]float64{
			0:   0.0,
			255: 255.0,
		},
		0, 255,
	)

	// WHEN
	result := fan.GetFanRpmCurveData()

	// THEN
	assert.Equal(t, expectedFanCurve, *result)
}

func TestFileFan_GetCurveId(t *testing.T) {
	// GIVEN
	curveId := "curveId"
	config := configuration.FanConfig{
		Curve: curveId,
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result := fan.GetCurveId()

	// THEN
	assert.Equal(t, curveId, result)
}

func TestFileFan_ShouldNeverStop(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		NeverStop: true,
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result := fan.ShouldNeverStop()

	// THEN
	assert.Equal(t, true, result)
}

func TestFileFan_GetPwmEnabled(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetPwmEnabled()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 1, result)
}

func TestFileFan_SetPwmEnabled(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	err := fan.SetPwmEnabled(ControlModeDisabled)

	// THEN
	assert.NoError(t, err)

	result, err := fan.GetPwmEnabled()
	assert.NoError(t, err)
	// NOTE: file fan does not support setting pwm enabled
	assert.Equal(t, 1, result)
}

func TestFileFan_IsPwmAuto(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.IsPwmAuto()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, true, result)
}

func TestFileFan_Supports_ControlMode(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result := fan.Supports(FeatureControlMode)

	// THEN
	assert.Equal(t, false, result)
}

func TestFileFan_Supports_RpmSensor_True(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "../../test/file_fan_pwm",
			RpmPath: "../../test/file_fan_rpm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result := fan.Supports(FeatureRpmSensor)

	// THEN
	assert.Equal(t, true, result)
}

func TestFileFan_Supports_RpmSensor_False(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path: "../../test/file_fan_pwm",
		},
	}

	fan, _ := NewFan(config)

	// WHEN
	result := fan.Supports(FeatureRpmSensor)

	// THEN
	assert.Equal(t, false, result)
}
