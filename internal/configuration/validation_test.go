package configuration

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/nvidia_base"
	"github.com/pterm/pterm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestValidateDuplicateFanId(t *testing.T) {
	// GIVEN
	fanId := "fan"
	config := Configuration{
		Fans: []FanConfig{
			{
				ID:    fanId,
				Curve: "curve",
				HwMon: nil,
				File: &FileFanConfig{
					Path: "abc",
				},
			},
			{
				ID:    fanId,
				Curve: "curve",
				HwMon: nil,
				File: &FileFanConfig{
					Path: "abc",
				},
			},
		},
		Curves: []CurveConfig{
			{
				ID: "curve",
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
				Function: nil,
			},
		},
		Sensors: []SensorConfig{
			{
				ID: "sensor",
				File: &FileSensorConfig{
					Path: "",
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, fmt.Sprintf("duplicate fan id detected: %s", fanId))
}

func TestValidateFanSubConfigIsMissing(t *testing.T) {
	// GIVEN
	config := Configuration{
		Fans: []FanConfig{
			{
				ID:    "fan",
				Curve: "curve",
				HwMon: nil,
				File:  nil,
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "fan fan: sub-configuration for fan is missing, use one of: hwmon | nvidia | file | cmd")
}

func TestValidateFanCurveWithIdIsNotDefined(t *testing.T) {
	// GIVEN
	config := Configuration{
		Fans: []FanConfig{
			{
				ID:        "fan",
				NeverStop: false,
				Curve:     "curve",
				File: &FileFanConfig{
					Path: "",
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "fan fan: no curve definition with id 'curve' found")
}

func TestValidateCurveSubConfigSensorIdIsMissing(t *testing.T) {
	// GIVEN
	config := Configuration{
		Curves: []CurveConfig{
			{
				ID:       "curve",
				Linear:   nil,
				Function: nil,
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "curve curve: sub-configuration for curve is missing, use one of: linear | pid | function")
}

func TestValidateCurveSensorIdIsMissing(t *testing.T) {
	// GIVEN
	config := Configuration{
		Curves: []CurveConfig{
			{
				ID: "curve",
				Linear: &LinearCurveConfig{
					Sensor: "",
					Min:    0,
					Max:    100,
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "curve curve: missing sensorId")
}

func TestValidateCurveSensorWithIdIsNotDefined(t *testing.T) {
	// GIVEN
	config := Configuration{
		Curves: []CurveConfig{
			{
				ID: "curve",
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "curve curve: no sensor definition with id 'sensor' found")
}

func TestValidateCurveDependencyToSelf(t *testing.T) {
	// GIVEN
	config := Configuration{
		Curves: []CurveConfig{
			{
				ID: "curve0",
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
			},
			{
				ID: "curve",
				Function: &FunctionCurveConfig{
					Type: FunctionAverage,
					Curves: []string{
						"curve",
						"curve0",
					},
				},
			},
		},
		Sensors: []SensorConfig{
			{
				ID: "sensor",
				File: &FileSensorConfig{
					// TODO: path empty validation
					Path: "",
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "curve curve: a curve cannot reference itself")
}

func TestValidateCurveDependencyCycle(t *testing.T) {
	// GIVEN
	config := Configuration{
		Curves: []CurveConfig{
			{
				ID: "curve0",
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
			},
			{
				ID: "curve1",
				Function: &FunctionCurveConfig{
					Type: FunctionAverage,
					Curves: []string{
						"curve2",
						"curve0",
					},
				},
			},
			{
				ID: "curve2",
				Function: &FunctionCurveConfig{
					Type: FunctionAverage,
					Curves: []string{
						"curve1",
						"curve0",
					},
				},
			},
		},
		Sensors: []SensorConfig{
			{
				ID: "sensor",
				File: &FileSensorConfig{
					// TODO: path empty validation
					Path: "",
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.Contains(t, err.Error(), "you have created a curve dependency cycle")
	// the order of these items is sometimes different, so we use this
	// "manual" check to avoid a flaky test
	assert.Contains(t, err.Error(), "curve1")
	assert.Contains(t, err.Error(), "curve2")
}

func TestValidateCurveDependencyWithIdIsNotDefined(t *testing.T) {
	// GIVEN
	config := Configuration{
		Curves: []CurveConfig{
			{
				ID: "curve0",
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
			},
			{
				ID: "curve1",
				Function: &FunctionCurveConfig{
					Type: FunctionAverage,
					Curves: []string{
						"curve0",
						"curve2",
					},
				},
			},
		},
		Sensors: []SensorConfig{
			{
				ID: "sensor",
				File: &FileSensorConfig{
					// TODO: path empty validation
					Path: "",
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "curve curve1: no curve definition with id 'curve2' found")
}

func TestValidateDuplicateCurveId(t *testing.T) {
	// GIVEN
	curveId := "curve"
	config := Configuration{
		Curves: []CurveConfig{
			{
				ID: curveId,
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
			},
			{
				ID: curveId,
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
			},
		},
		Sensors: []SensorConfig{
			{
				ID: "sensor",
				File: &FileSensorConfig{
					// TODO: path empty validation
					Path: "",
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, fmt.Sprintf("duplicate curve id detected: %s", curveId))
}

func TestValidateCurve(t *testing.T) {
	// GIVEN
	config := Configuration{
		Curves: []CurveConfig{
			{
				ID: "curve",
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
			},
		},
		Sensors: []SensorConfig{
			{
				ID: "sensor",
				File: &FileSensorConfig{
					// TODO: path empty validation
					Path: "",
				},
			},
		},
	}

	setValidGlobals(&config)

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.NoError(t, err)
}

func TestValidateCurveFunctionTypeUnsupported(t *testing.T) {
	// GIVEN
	config := Configuration{
		Curves: []CurveConfig{
			{
				ID: "curve1",
				Function: &FunctionCurveConfig{
					Type: "unsupported",
					Curves: []string{
						"curve2",
					},
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "curve curve1: unsupported function type 'unsupported', use one of: minimum | average | maximum | delta | sum | difference")
}

func TestValidateSensorSubConfigSensorIdIsMissing(t *testing.T) {
	// GIVEN
	config := Configuration{
		Sensors: []SensorConfig{
			{
				ID: "sensor",
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "sensor sensor: sub-configuration for sensor is missing, use one of: hwmon | nvidia | file | cmd | disk")
}

func TestValidateSensor(t *testing.T) {
	// GIVEN
	config := Configuration{
		Sensors: []SensorConfig{
			{
				ID: "sensor",
				File: &FileSensorConfig{
					Path: "",
				},
			},
		},
	}
	setValidGlobals(&config)

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.NoError(t, err)
}

func TestValidateDuplicateSensorId(t *testing.T) {
	// GIVEN
	sensorId := "sensor"
	config := Configuration{
		Sensors: []SensorConfig{
			{
				ID: sensorId,
				File: &FileSensorConfig{
					Path: "",
				},
			},
			{
				ID: sensorId,
				File: &FileSensorConfig{
					Path: "",
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, fmt.Sprintf("duplicate sensor id detected: %s", sensorId))
}

func TestValidateFanHasIndexOrChannel(t *testing.T) {
	// GIVEN
	config := Configuration{
		Fans: []FanConfig{
			{
				ID:    "fan",
				Curve: "curve",
				HwMon: &HwMonFanConfig{},
			},
		},
		Curves: []CurveConfig{
			{
				ID: "curve",
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
				Function: nil,
			},
		},
		Sensors: []SensorConfig{
			{
				ID: "sensor",
				File: &FileSensorConfig{
					Path: "",
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "fan fan: must have one of index or rpmChannel, must be >= 1")
}

func TestValidateFanIndex(t *testing.T) {
	// GIVEN
	config := Configuration{
		Fans: []FanConfig{
			{
				ID:    "fan",
				Curve: "curve",
				HwMon: &HwMonFanConfig{
					Index: -1,
				},
			},
		},
		Curves: []CurveConfig{
			{
				ID: "curve",
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
				Function: nil,
			},
		},
		Sensors: []SensorConfig{
			{
				ID: "sensor",
				File: &FileSensorConfig{
					Path: "",
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "fan fan: invalid index, must be >= 1")
}

func TestValidateFanChannel(t *testing.T) {
	// GIVEN
	config := Configuration{
		Fans: []FanConfig{
			{
				ID:    "fan",
				Curve: "curve",
				HwMon: &HwMonFanConfig{
					RpmChannel: -1,
				},
			},
		},
		Curves: []CurveConfig{
			{
				ID: "curve",
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
				Function: nil,
			},
		},
		Sensors: []SensorConfig{
			{
				ID: "sensor",
				File: &FileSensorConfig{
					Path: "",
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "fan fan: invalid rpmChannel, must be >= 1")
}

func TestValidateFanPwmChannel(t *testing.T) {
	// GIVEN
	config := Configuration{
		Fans: []FanConfig{
			{
				ID:    "fan",
				Curve: "curve",
				HwMon: &HwMonFanConfig{
					RpmChannel: 1,
					PwmChannel: -1,
				},
			},
		},
		Curves: []CurveConfig{
			{
				ID: "curve",
				Linear: &LinearCurveConfig{
					Sensor: "sensor",
					Min:    0,
					Max:    100,
				},
				Function: nil,
			},
		},
		Sensors: []SensorConfig{
			{
				ID: "sensor",
				File: &FileSensorConfig{
					Path: "",
				},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "fan fan: invalid pwmChannel, must be >= 1")
}

// setValidGlobals sets the global settings to their defaults, so that
// ValidateConfig accepts a config built as a struct literal.
func setValidGlobals(config *Configuration) {
	config.TempSensorPollingRate = 200 * time.Millisecond
	config.TempRollingWindowSize = 10
	config.RpmPollingRate = 1 * time.Second
	config.RpmRollingWindowSize = 10
	config.FanController.AdjustmentTickRate = 200 * time.Millisecond
	config.Analysis.SampleCount = 3
}

// helper: minimal valid config with a file fan for PwmMap validation tests
func minimalFanConfig(pwmMap *PwmMapConfig) Configuration {
	return withValidGlobals(Configuration{
		Fans: []FanConfig{
			{
				ID:     "fan",
				Curve:  "curve",
				File:   &FileFanConfig{Path: "/dev/null"},
				PwmMap: pwmMap,
			},
		},
		Curves: []CurveConfig{
			{
				ID:     "curve",
				Linear: &LinearCurveConfig{Sensor: "sensor", Min: 0, Max: 100},
			},
		},
		Sensors: []SensorConfig{
			{ID: "sensor", File: &FileSensorConfig{Path: ""}},
		},
	})
}

func withValidGlobals(config Configuration) Configuration {
	setValidGlobals(&config)
	return config
}

func TestValidatePwmMap_EmptyStruct(t *testing.T) {
	// PwmMapConfig with no sub-config set should fail
	cfg := minimalFanConfig(&PwmMapConfig{})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': pwmMap is set but no mode is specified")
}

func TestValidatePwmMap_MultipleModesSet(t *testing.T) {
	// More than one sub-config set should fail
	cfg := minimalFanConfig(&PwmMapConfig{
		Autodetect: &PwmMapAutodetectConfig{},
		Identity:   &PwmMapIdentityConfig{},
	})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': only one pwmMap mode can be configured at a time")
}

func TestValidatePwmMap_LinearEmptyPoints(t *testing.T) {
	empty := PwmMapLinearConfig{}
	cfg := minimalFanConfig(&PwmMapConfig{Linear: &empty})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': pwmMap linear requires at least one control point")
}

func TestValidatePwmMap_ValuesEmptyPoints(t *testing.T) {
	empty := PwmMapValuesConfig{}
	cfg := minimalFanConfig(&PwmMapConfig{Values: &empty})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': pwmMap values requires at least one control point")
}

func TestValidatePwmMap_NonMonotonicValues(t *testing.T) {
	pts := PwmMapValuesConfig{0: 0, 128: 200, 255: 100}
	cfg := minimalFanConfig(&PwmMapConfig{Values: &pts})
	err := ValidateConfig(&cfg, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "strictly monotonically increasing")
}

func TestValidatePwmMap_KeyOutOfRange(t *testing.T) {
	pts := PwmMapValuesConfig{0: 0, 300: 100}
	cfg := minimalFanConfig(&PwmMapConfig{Values: &pts})
	err := ValidateConfig(&cfg, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestValidatePwmMap_Autodetect(t *testing.T) {
	cfg := minimalFanConfig(&PwmMapConfig{Autodetect: &PwmMapAutodetectConfig{}})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidatePwmMap_Identity(t *testing.T) {
	cfg := minimalFanConfig(&PwmMapConfig{Identity: &PwmMapIdentityConfig{}})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidatePwmMap_ValidLinear(t *testing.T) {
	pts := PwmMapLinearConfig{0: 0, 255: 255}
	cfg := minimalFanConfig(&PwmMapConfig{Linear: &pts})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidatePwmMap_ValidValues(t *testing.T) {
	pts := PwmMapValuesConfig{0: 0, 64: 128, 192: 255}
	cfg := minimalFanConfig(&PwmMapConfig{Values: &pts})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidatePwmMap_Nil(t *testing.T) {
	// nil PwmMap (autodetect default) should pass validation
	cfg := minimalFanConfig(nil)
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

// helper: minimal valid config with a file fan for SetPwmToGetPwmMap validation tests
func minimalFanConfigWithSetPwm(setPwmToGetPwmMap *SetPwmToGetPwmMapConfig) Configuration {
	return withValidGlobals(Configuration{
		Fans: []FanConfig{
			{
				ID:                "fan",
				Curve:             "curve",
				File:              &FileFanConfig{Path: "/dev/null"},
				SetPwmToGetPwmMap: setPwmToGetPwmMap,
			},
		},
		Curves: []CurveConfig{
			{
				ID:     "curve",
				Linear: &LinearCurveConfig{Sensor: "sensor", Min: 0, Max: 100},
			},
		},
		Sensors: []SensorConfig{
			{ID: "sensor", File: &FileSensorConfig{Path: ""}},
		},
	})
}

func TestValidateSetPwmToGetPwmMap_EmptyStruct(t *testing.T) {
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': setPwmToGetPwmMap is set but no mode is specified")
}

func TestValidateSetPwmToGetPwmMap_MultipleModesSet(t *testing.T) {
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{
		Autodetect: &SetPwmToGetPwmMapAutodetectConfig{},
		Identity:   &SetPwmToGetPwmMapIdentityConfig{},
	})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': only one setPwmToGetPwmMap mode can be configured at a time")
}

func TestValidateSetPwmToGetPwmMap_LinearEmptyPoints(t *testing.T) {
	empty := SetPwmToGetPwmMapLinearConfig{}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Linear: &empty})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': setPwmToGetPwmMap linear requires at least one control point")
}

func TestValidateSetPwmToGetPwmMap_ValuesEmptyPoints(t *testing.T) {
	empty := SetPwmToGetPwmMapValuesConfig{}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Values: &empty})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': setPwmToGetPwmMap values requires at least one control point")
}

func TestValidateSetPwmToGetPwmMap_KeyOutOfRange(t *testing.T) {
	pts := SetPwmToGetPwmMapValuesConfig{0: 0, 300: 100}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Values: &pts})
	err := ValidateConfig(&cfg, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestValidateSetPwmToGetPwmMap_NonMonotonic(t *testing.T) {
	pts := SetPwmToGetPwmMapValuesConfig{0: 0, 128: 200, 255: 100}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Values: &pts})
	err := ValidateConfig(&cfg, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "monotonically increasing")
}

func TestValidateSetPwmToGetPwmMap_ValuesEqualAdjacentPointsAreAllowed(t *testing.T) {
	pts := SetPwmToGetPwmMapValuesConfig{0: 0, 128: 100, 255: 100}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Values: &pts})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateSetPwmToGetPwmMap_Autodetect(t *testing.T) {
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Autodetect: &SetPwmToGetPwmMapAutodetectConfig{}})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateSetPwmToGetPwmMap_Identity(t *testing.T) {
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Identity: &SetPwmToGetPwmMapIdentityConfig{}})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateSetPwmToGetPwmMap_ValidLinear(t *testing.T) {
	pts := SetPwmToGetPwmMapLinearConfig{0: 0, 255: 200}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Linear: &pts})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateSetPwmToGetPwmMap_ValidValues(t *testing.T) {
	pts := SetPwmToGetPwmMapValuesConfig{0: 0, 128: 100, 255: 200}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Values: &pts})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateSetPwmToGetPwmMap_Nil(t *testing.T) {
	// nil SetPwmToGetPwmMap (autodetect default) should pass validation
	cfg := minimalFanConfigWithSetPwm(nil)
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

// helper: build a minimal valid Configuration with a given ControlModeConfig
func minimalFanConfigWithControlMode(cm *ControlModeConfig) Configuration {
	return withValidGlobals(Configuration{
		Fans: []FanConfig{
			{
				ID:          "fan",
				Curve:       "curve",
				File:        &FileFanConfig{Path: "/dev/null"},
				ControlMode: cm,
			},
		},
		Curves: []CurveConfig{
			{ID: "curve", Linear: &LinearCurveConfig{Sensor: "sensor", Min: 0, Max: 100}},
		},
		Sensors: []SensorConfig{
			{ID: "sensor", File: &FileSensorConfig{Path: ""}},
		},
	})
}

func TestValidateControlMode_Nil(t *testing.T) {
	cfg := minimalFanConfigWithControlMode(nil)
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateControlMode_Active_ValidNames(t *testing.T) {
	for _, name := range []string{"pwm", "manual", "disabled", "auto", "automatic", "PWM", "Auto"} {
		v := ControlModeValue(name)
		cfg := minimalFanConfigWithControlMode(&ControlModeConfig{Active: &v})
		err := ValidateConfig(&cfg, "")
		assert.NoError(t, err, "expected no error for active=%q", name)
	}
}

func TestValidateControlMode_Active_ValidInteger(t *testing.T) {
	for _, s := range []string{"0", "1", "2", "99"} {
		v := ControlModeValue(s)
		cfg := minimalFanConfigWithControlMode(&ControlModeConfig{Active: &v})
		err := ValidateConfig(&cfg, "")
		assert.NoError(t, err, "expected no error for active=%q", s)
	}
}

func TestValidateControlMode_Active_Invalid(t *testing.T) {
	v := ControlModeValue("bogus")
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{Active: &v})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, `fan 'fan': invalid controlMode.active "bogus" (valid: auto, pwm, disabled, or integer)`)
}

func TestValidateControlMode_OnExit_Restore(t *testing.T) {
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{Restore: &OnExitRestoreConfig{}},
	})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateControlMode_OnExit_None(t *testing.T) {
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{None: &OnExitNoneConfig{}},
	})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateControlMode_OnExit_ControlModeOnly(t *testing.T) {
	v := ControlModeValue("auto")
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{ControlMode: &v},
	})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateControlMode_OnExit_SpeedOnly(t *testing.T) {
	speed := 128
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{Speed: &speed},
	})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateControlMode_OnExit_ControlModeAndSpeed(t *testing.T) {
	v := ControlModeValue("pwm")
	speed := 128
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{ControlMode: &v, Speed: &speed},
	})
	err := ValidateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateControlMode_OnExit_Empty(t *testing.T) {
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{},
	})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': controlMode.onExit is set but no option is specified")
}

func TestValidateControlMode_OnExit_RestoreAndSpeed(t *testing.T) {
	speed := 128
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{Restore: &OnExitRestoreConfig{}, Speed: &speed},
	})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': controlMode.onExit restore/none cannot be combined with mode/speed")
}

func TestValidateControlMode_OnExit_NoneAndControlMode(t *testing.T) {
	v := ControlModeValue("auto")
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{None: &OnExitNoneConfig{}, ControlMode: &v},
	})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': controlMode.onExit restore/none cannot be combined with mode/speed")
}

func TestValidateControlMode_OnExit_RestoreAndNone(t *testing.T) {
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{Restore: &OnExitRestoreConfig{}, None: &OnExitNoneConfig{}},
	})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': controlMode.onExit restore and none cannot be combined")
}

func TestValidateControlMode_OnExit_InvalidControlMode(t *testing.T) {
	v := ControlModeValue("bogus")
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{ControlMode: &v},
	})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, `fan 'fan': invalid controlMode.onExit.mode "bogus" (valid: auto, pwm, disabled, or integer)`)
}

func TestValidateControlMode_OnExit_SpeedBelowZero(t *testing.T) {
	speed := -1
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{Speed: &speed},
	})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': controlMode.onExit.speed must be in [0..255], got -1")
}

func TestValidateControlMode_OnExit_SpeedAbove255(t *testing.T) {
	speed := 256
	cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
		OnExit: &OnExitConfig{Speed: &speed},
	})
	err := ValidateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': controlMode.onExit.speed must be in [0..255], got 256")
}

func TestValidateControlMode_OnExit_SpeedBoundaryValues(t *testing.T) {
	for _, speed := range []int{0, 128, 255} {
		s := speed
		cfg := minimalFanConfigWithControlMode(&ControlModeConfig{
			OnExit: &OnExitConfig{Speed: &s},
		})
		err := ValidateConfig(&cfg, "")
		assert.NoError(t, err, "expected no error for speed=%d", speed)
	}
}

func TestValidateDiskSensor_Valid(t *testing.T) {
	// GIVEN
	config := Configuration{
		Sensors: []SensorConfig{
			{
				ID:   "disk_temp",
				Disk: &DiskSensorConfig{Device: "/dev/sda"},
			},
		},
	}
	setValidGlobals(&config)

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.NoError(t, err)
}

func TestValidateDiskSensor_MissingDevice(t *testing.T) {
	// GIVEN
	config := Configuration{
		Sensors: []SensorConfig{
			{
				ID:   "disk_temp",
				Disk: &DiskSensorConfig{Device: ""},
			},
		},
	}

	// WHEN
	err := ValidateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "sensor disk_temp: disk sensor requires a device path")
}

func TestFanConfig_PwmSetDelay_Set(t *testing.T) {
	// GIVEN
	d := 10 * time.Millisecond

	// WHEN
	fanConfig := FanConfig{PwmSetDelay: &d}

	// THEN
	assert.NotNil(t, fanConfig.PwmSetDelay)
	assert.Equal(t, 10*time.Millisecond, *fanConfig.PwmSetDelay)
}

func TestFanConfig_PwmSetDelay_Absent(t *testing.T) {
	// GIVEN / WHEN
	fanConfig := FanConfig{}

	// THEN
	assert.Nil(t, fanConfig.PwmSetDelay)
}

// loadAndValidateYaml loads the given YAML like the daemon does on startup and on reload
// (defaults, LoadConfig, ValidateConfig) and returns the first error.
func loadAndValidateYaml(t *testing.T, yaml string) error {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	setDefaultValues()
	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(strings.NewReader(yaml)); err != nil {
		t.Fatalf("invalid test YAML: %v", err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	return ValidateConfig(&cfg, "")
}

// validationGapsYaml builds a minimal valid config. globals is inserted at the top level,
// fanExtra into the fan entry and curves replaces the curve list.
func validationGapsYaml(globals, fanExtra, curves string) string {
	if curves == "" {
		curves = `
  - id: curve
    linear:
      sensor: sensor
      steps:
        - 40: 0
        - 80: 255
`
	}
	return fmt.Sprintf(`
%s
sensors:
  - id: sensor
    file:
      path: /dev/null
curves:
%s
fans:
  - id: fan
    curve: curve
    file:
      path: /dev/null
%s
`, globals, curves, fanExtra)
}

// TestValidateConfig_ValidationGaps covers values that used to load without error but
// crash the daemon (ticker panics), produce NaN/Inf averages or invert the PWM scaling.
func TestValidateConfig_ValidationGaps(t *testing.T) {
	tests := []struct {
		name     string
		globals  string
		fanExtra string
		curves   string
		// wantErr is a substring of the expected error, empty means no error
		wantErr string
		// wantWarning is a substring of an expected warning
		wantWarning string
	}{
		{name: "valid base config"},
		{name: "adjustmentTickRate zero", globals: "fanController:\n  adjustmentTickRate: 0s", wantErr: "fanController.adjustmentTickRate must be > 0, got 0s"},
		{name: "adjustmentTickRate negative", globals: "fanController:\n  adjustmentTickRate: -1s", wantErr: "fanController.adjustmentTickRate must be > 0, got -1s"},
		{name: "deprecated controllerAdjustmentTickRate is applied before validation", globals: "controllerAdjustmentTickRate: 500ms\nfanController:\n  adjustmentTickRate: 0s"},
		{name: "rpmPollingRate zero", globals: "rpmPollingRate: 0s", wantErr: "rpmPollingRate must be > 0, got 0s"},
		{name: "tempSensorPollingRate zero", globals: "tempSensorPollingRate: 0s", wantErr: "tempSensorPollingRate must be > 0, got 0s"},
		{name: "tempRollingWindowSize zero", globals: "tempRollingWindowSize: 0", wantErr: "tempRollingWindowSize must be >= 1, got 0"},
		{name: "rpmRollingWindowSize zero", globals: "rpmRollingWindowSize: 0", wantErr: "rpmRollingWindowSize must be >= 1, got 0"},
		{name: "fanController.pwmSetDelay negative", globals: "fanController:\n  pwmSetDelay: -1ms", wantErr: "fanController.pwmSetDelay must be >= 0, got -1ms"},
		{name: "fanController.pwmSetDelay zero is allowed", globals: "fanController:\n  pwmSetDelay: 0s"},
		{name: "fan pwmSetDelay negative", fanExtra: "    pwmSetDelay: -1ms", wantErr: "fan fan: pwmSetDelay must be >= 0, got -1ms"},
		{name: "fanResponseDelay negative", globals: "fanResponseDelay: -1", wantErr: "fanResponseDelay must be >= 0, got -1s"},
		{name: "fanResponseDelay negative duration", globals: "fanResponseDelay: -500ms", wantErr: "fanResponseDelay must be >= 0, got -500ms"},
		{name: "analysis.sampleCount zero", globals: "analysis:\n  sampleCount: 0", wantErr: "analysis.sampleCount must be >= 1, got 0"},
		{name: "analysis.sampleDelay negative", globals: "analysis:\n  sampleDelay: -1ms", wantErr: "analysis.sampleDelay must be >= 0, got -1ms"},
		{name: "analysis.settleTimeout negative", globals: "analysis:\n  settleTimeout: -1s", wantErr: "analysis.settleTimeout must be >= 0, got -1s"},
		{name: "maxRpmDiffForSettledFan negative", globals: "maxRpmDiffForSettledFan: -1", wantErr: "maxRpmDiffForSettledFan must be >= 0, got -1"},
		{name: "minPwm out of range", fanExtra: "    minPwm: 256", wantErr: "fan fan: minPwm must be in [0..255], got 256"},
		{name: "startPwm out of range", fanExtra: "    startPwm: 300", wantErr: "fan fan: startPwm must be in [0..255], got 300"},
		{name: "maxPwm out of range", fanExtra: "    maxPwm: -1", wantErr: "fan fan: maxPwm must be in [0..255], got -1"},
		{name: "minPwm, startPwm and maxPwm inconsistent", fanExtra: "    minPwm: 200\n    maxPwm: 100\n    startPwm: 300", wantErr: "fan fan: startPwm must be in [0..255], got 300"},
		{name: "minPwm > maxPwm", fanExtra: "    minPwm: 200\n    maxPwm: 100", wantErr: "fan fan: minPwm (200) must be <= maxPwm (100)"},
		{name: "startPwm > maxPwm", fanExtra: "    startPwm: 200\n    maxPwm: 100", wantErr: "fan fan: startPwm (200) must be <= maxPwm (100)"},
		{name: "minPwm == startPwm == maxPwm is allowed", fanExtra: "    minPwm: 100\n    startPwm: 100\n    maxPwm: 100"},
		{name: "minPwm > startPwm is only a warning", fanExtra: "    minPwm: 100\n    startPwm: 50", wantWarning: "fan fan: suspicious pwm config: minPwm (100) > startPwm (50)"},
		{name: "pwmMap linear value out of range", fanExtra: "    pwmMap:\n      linear:\n        0: 0\n        128: 999", wantErr: "fan 'fan': pwmMap linear value 999 (key 128) is out of range [0..255]"},
		{name: "pwmMap values value out of range", fanExtra: "    pwmMap:\n      values:\n        0: -1\n        255: 255", wantErr: "fan 'fan': pwmMap values value -1 (key 0) is out of range [0..255]"},
		{name: "setPwmToGetPwmMap linear value out of range", fanExtra: "    setPwmToGetPwmMap:\n      linear:\n        0: 0\n        255: 256", wantErr: "fan 'fan': setPwmToGetPwmMap linear value 256 (key 255) is out of range [0..255]"},
		{name: "linear curve step NaN", curves: "  - id: curve\n    linear:\n      sensor: sensor\n      steps:\n        - 40: NaN\n        - 80: 255", wantErr: "invalid curve step value 'NaN'"},
		{name: "linear curve step nan%", curves: "  - id: curve\n    linear:\n      sensor: sensor\n      steps:\n        - 40: nan%\n        - 80: 255", wantErr: "invalid curve step value 'nan%'"},
		{name: "linear curve step Inf", curves: "  - id: curve\n    linear:\n      sensor: sensor\n      steps:\n        - 40: 0\n        - 80: Inf", wantErr: "invalid curve step value 'Inf'"},
		{name: "staircase curve step NaN", curves: "  - id: curve\n    staircase:\n      sensor: sensor\n      steps:\n        - 40: NaN\n        - 80: 255", wantErr: "invalid curve step value 'NaN'"},
		{name: "pid curve setPoint NaN", curves: "  - id: curve\n    pid:\n      sensor: sensor\n      setPoint: .nan\n      p: -0.05\n      i: -0.005\n      d: -0.005", wantErr: "curve curve: setPoint must be a finite number, got NaN"},
		{name: "pid curve setPoint Inf", curves: "  - id: curve\n    pid:\n      sensor: sensor\n      setPoint: .inf\n      p: -0.05\n      i: -0.005\n      d: -0.005", wantErr: "curve curve: setPoint must be a finite number, got +Inf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var warnings bytes.Buffer
			oldWarning := pterm.Warning
			pterm.Warning = *pterm.Warning.WithWriter(&warnings)
			t.Cleanup(func() { pterm.Warning = oldWarning })

			err := loadAndValidateYaml(t, validationGapsYaml(tt.globals, tt.fanExtra, tt.curves))

			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
			if tt.wantWarning != "" {
				assert.Contains(t, warnings.String(), tt.wantWarning)
			} else {
				assert.NotContains(t, warnings.String(), "suspicious pwm config")
			}
		})
	}
}

func TestValidateNvidiaDevice(t *testing.T) {
	tests := []struct {
		name    string
		device  string
		wantErr string
	}{
		{name: "identifier", device: "nvidia-10DE2489-0800"},
		{name: "regex", device: "nvidia-10DE.*"},
		{name: "empty", device: "", wantErr: "nvidia.device must not be empty"},
		{name: "whitespace", device: "  ", wantErr: "nvidia.device must not be empty"},
		{name: "invalid regex", device: "nvidia-(", wantErr: "nvidia.device is not a valid regex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNvidiaDevice(tt.device)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateConfig_NvidiaDeviceIsEmpty(t *testing.T) {
	if !nvidia_base.IsNvmlSupported {
		t.Skip("built without nvml support, nvidia configs are rejected before the device is checked")
	}

	t.Run("fan", func(t *testing.T) {
		config := Configuration{
			Fans: []FanConfig{
				{ID: "gpu_fan", Curve: "curve", Nvidia: &NvidiaFanConfig{Device: "", Index: 1}},
			},
			Curves: []CurveConfig{
				{ID: "curve", Linear: &LinearCurveConfig{Sensor: "sensor", Min: 0, Max: 100}},
			},
			Sensors: []SensorConfig{
				{ID: "sensor", File: &FileSensorConfig{Path: "/tmp/temp"}},
			},
		}

		err := ValidateConfig(&config, "")

		assert.EqualError(t, err, "fan gpu_fan: nvidia.device must not be empty, use the identifier shown by 'fan2go detect', e.g. nvidia-10DE2489-0800")
	})

	t.Run("sensor", func(t *testing.T) {
		config := Configuration{
			Sensors: []SensorConfig{
				{ID: "gpu_temp", Nvidia: &NvidiaSensorConfig{Device: "", Index: 1}},
			},
		}

		err := ValidateConfig(&config, "")

		assert.EqualError(t, err, "sensor gpu_temp: nvidia.device must not be empty, use the identifier shown by 'fan2go detect', e.g. nvidia-10DE2489-0800")
	})
}
