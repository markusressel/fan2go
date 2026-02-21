package configuration

import (
	"fmt"
	"testing"

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
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "curve curve: no sensor definition with id 'sensor' found")
}

func TestValidateCurveDependencyToSelf(t *testing.T) {
	// GIVEN
	config := Configuration{
		Curves: []CurveConfig{
			{
				ID: "curve",
				Function: &FunctionCurveConfig{
					Type: FunctionAverage,
					Curves: []string{
						"curve",
					},
				},
			},
		},
	}

	// WHEN
	err := validateConfig(&config, "")

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
					},
				},
			},
			{
				ID: "curve2",
				Function: &FunctionCurveConfig{
					Type: FunctionAverage,
					Curves: []string{
						"curve1",
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
	err := validateConfig(&config, "")

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
				ID: "curve1",
				Function: &FunctionCurveConfig{
					Type: FunctionAverage,
					Curves: []string{
						"curve2",
					},
				},
			},
		},
	}

	// WHEN
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

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

	// WHEN
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "sensor sensor: sub-configuration for sensor is missing, use one of: hwmon | nvidia | file | cmd")
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

	// WHEN
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

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
	err := validateConfig(&config, "")

	// THEN
	assert.EqualError(t, err, "fan fan: invalid pwmChannel, must be >= 1")
}

// helper: minimal valid config with a file fan for PwmMap validation tests
func minimalFanConfig(pwmMap *PwmMapConfig) Configuration {
	return Configuration{
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
	}
}

func TestValidatePwmMap_EmptyStruct(t *testing.T) {
	// PwmMapConfig with no sub-config set should fail
	cfg := minimalFanConfig(&PwmMapConfig{})
	err := validateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': pwmMap is set but no mode is specified")
}

func TestValidatePwmMap_MultipleModesSet(t *testing.T) {
	// More than one sub-config set should fail
	cfg := minimalFanConfig(&PwmMapConfig{
		Autodetect: &PwmMapAutodetectConfig{},
		Identity:   &PwmMapIdentityConfig{},
	})
	err := validateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': only one pwmMap mode can be configured at a time")
}

func TestValidatePwmMap_LinearEmptyPoints(t *testing.T) {
	empty := PwmMapLinearConfig{}
	cfg := minimalFanConfig(&PwmMapConfig{Linear: &empty})
	err := validateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': pwmMap linear requires at least one control point")
}

func TestValidatePwmMap_ValuesEmptyPoints(t *testing.T) {
	empty := PwmMapValuesConfig{}
	cfg := minimalFanConfig(&PwmMapConfig{Values: &empty})
	err := validateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': pwmMap values requires at least one control point")
}

func TestValidatePwmMap_NonMonotonicValues(t *testing.T) {
	pts := PwmMapValuesConfig{0: 0, 128: 200, 255: 100}
	cfg := minimalFanConfig(&PwmMapConfig{Values: &pts})
	err := validateConfig(&cfg, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "strictly monotonically increasing")
}

func TestValidatePwmMap_KeyOutOfRange(t *testing.T) {
	pts := PwmMapValuesConfig{0: 0, 300: 100}
	cfg := minimalFanConfig(&PwmMapConfig{Values: &pts})
	err := validateConfig(&cfg, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestValidatePwmMap_Autodetect(t *testing.T) {
	cfg := minimalFanConfig(&PwmMapConfig{Autodetect: &PwmMapAutodetectConfig{}})
	err := validateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidatePwmMap_Identity(t *testing.T) {
	cfg := minimalFanConfig(&PwmMapConfig{Identity: &PwmMapIdentityConfig{}})
	err := validateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidatePwmMap_ValidLinear(t *testing.T) {
	pts := PwmMapLinearConfig{0: 0, 255: 255}
	cfg := minimalFanConfig(&PwmMapConfig{Linear: &pts})
	err := validateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidatePwmMap_ValidValues(t *testing.T) {
	pts := PwmMapValuesConfig{0: 0, 64: 128, 192: 255}
	cfg := minimalFanConfig(&PwmMapConfig{Values: &pts})
	err := validateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidatePwmMap_Nil(t *testing.T) {
	// nil PwmMap (autodetect default) should pass validation
	cfg := minimalFanConfig(nil)
	err := validateConfig(&cfg, "")
	assert.NoError(t, err)
}

// helper: minimal valid config with a file fan for SetPwmToGetPwmMap validation tests
func minimalFanConfigWithSetPwm(setPwmToGetPwmMap *SetPwmToGetPwmMapConfig) Configuration {
	return Configuration{
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
	}
}

func TestValidateSetPwmToGetPwmMap_EmptyStruct(t *testing.T) {
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{})
	err := validateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': setPwmToGetPwmMap is set but no mode is specified")
}

func TestValidateSetPwmToGetPwmMap_MultipleModesSet(t *testing.T) {
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{
		Autodetect: &SetPwmToGetPwmMapAutodetectConfig{},
		Identity:   &SetPwmToGetPwmMapIdentityConfig{},
	})
	err := validateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': only one setPwmToGetPwmMap mode can be configured at a time")
}

func TestValidateSetPwmToGetPwmMap_LinearEmptyPoints(t *testing.T) {
	empty := SetPwmToGetPwmMapLinearConfig{}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Linear: &empty})
	err := validateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': setPwmToGetPwmMap linear requires at least one control point")
}

func TestValidateSetPwmToGetPwmMap_ValuesEmptyPoints(t *testing.T) {
	empty := SetPwmToGetPwmMapValuesConfig{}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Values: &empty})
	err := validateConfig(&cfg, "")
	assert.EqualError(t, err, "fan 'fan': setPwmToGetPwmMap values requires at least one control point")
}

func TestValidateSetPwmToGetPwmMap_KeyOutOfRange(t *testing.T) {
	pts := SetPwmToGetPwmMapValuesConfig{0: 0, 300: 100}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Values: &pts})
	err := validateConfig(&cfg, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestValidateSetPwmToGetPwmMap_NonMonotonic(t *testing.T) {
	pts := SetPwmToGetPwmMapValuesConfig{0: 0, 128: 200, 255: 100}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Values: &pts})
	err := validateConfig(&cfg, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "strictly monotonically increasing")
}

func TestValidateSetPwmToGetPwmMap_Autodetect(t *testing.T) {
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Autodetect: &SetPwmToGetPwmMapAutodetectConfig{}})
	err := validateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateSetPwmToGetPwmMap_Identity(t *testing.T) {
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Identity: &SetPwmToGetPwmMapIdentityConfig{}})
	err := validateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateSetPwmToGetPwmMap_ValidLinear(t *testing.T) {
	pts := SetPwmToGetPwmMapLinearConfig{0: 0, 255: 200}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Linear: &pts})
	err := validateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateSetPwmToGetPwmMap_ValidValues(t *testing.T) {
	pts := SetPwmToGetPwmMapValuesConfig{0: 0, 128: 100, 255: 200}
	cfg := minimalFanConfigWithSetPwm(&SetPwmToGetPwmMapConfig{Values: &pts})
	err := validateConfig(&cfg, "")
	assert.NoError(t, err)
}

func TestValidateSetPwmToGetPwmMap_Nil(t *testing.T) {
	// nil SetPwmToGetPwmMap (autodetect default) should pass validation
	cfg := minimalFanConfigWithSetPwm(nil)
	err := validateConfig(&cfg, "")
	assert.NoError(t, err)
}
