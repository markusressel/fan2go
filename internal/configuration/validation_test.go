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
	assert.EqualError(t, err, fmt.Sprintf("Duplicate fan id detected: %s", fanId))
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
	assert.EqualError(t, err, "Fan fan: sub-configuration for fan is missing, use one of: hwmon | file | cmd")
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
	assert.EqualError(t, err, "Fan fan: no curve definition with id 'curve' found")
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
	assert.EqualError(t, err, "Curve curve: sub-configuration for curve is missing, use one of: linear | pid | function")
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
	assert.EqualError(t, err, "Curve curve: Missing sensorId")
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
	assert.EqualError(t, err, "Curve curve: no sensor definition with id 'sensor' found")
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
	assert.EqualError(t, err, "Curve curve: a curve cannot reference itself")
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
	assert.Contains(t, err.Error(), "You have created a curve dependency cycle")
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
	assert.EqualError(t, err, "Curve curve1: no curve definition with id 'curve2' found")
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
	assert.EqualError(t, err, fmt.Sprintf("Duplicate curve id detected: %s", curveId))
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
	assert.EqualError(t, err, "Curve curve1: unsupported function type 'unsupported', use one of: minimum | average | maximum | delta | sum | difference")
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
	assert.EqualError(t, err, "Sensor sensor: sub-configuration for sensor is missing, use one of: hwmon | file | cmd")
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
	assert.EqualError(t, err, fmt.Sprintf("Duplicate sensor id detected: %s", sensorId))
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
	assert.EqualError(t, err, "Fan fan: must have one of index or channel, must be >= 1")
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
	assert.EqualError(t, err, "Fan fan: invalid index, must be >= 1")
}

func TestValidateFanChannel(t *testing.T) {
	// GIVEN
	config := Configuration{
		Fans: []FanConfig{
			{
				ID:    "fan",
				Curve: "curve",
				HwMon: &HwMonFanConfig{
					Channel: -1,
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
	assert.EqualError(t, err, "Fan fan: invalid channel, must be >= 1")
}

func TestValidateFanPwmChannel(t *testing.T) {
	// GIVEN
	config := Configuration{
		Fans: []FanConfig{
			{
				ID:    "fan",
				Curve: "curve",
				HwMon: &HwMonFanConfig{
					Channel:    1,
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
	assert.EqualError(t, err, "Fan fan: invalid pwmChannel, must be >= 1")
}
