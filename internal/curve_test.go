package internal

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestFunctionCurve(t *testing.T) {
	// GIVEN
	function := "average"
	curves := []CurveConfig{
		{
			Id:   "case_fan_front",
			Type: "linear",
			Params: LinearCurveConfig{
				MinTemp: 40,
				MaxTemp: 255,
				Steps: map[int]int{
					10:  10,
					255: 255,
				},
			},
		},
		{
			Id:   "case_fan_back",
			Type: "linear",
			Params: LinearCurveConfig{
				MinTemp: 40,
				MaxTemp: 255,
			},
		},
	}

	// WHEN
	var curveValues []int
	for _, curve := range curves {
		evaluated, err := evaluateCurve(curve)
		if err != nil {
			assert.Fail(t, err.Error())
		}
		curveValues = append(curveValues, evaluated)
	}

	result := FunctionCurve(function, curveValues)

	// THEN
	assert.Equal(t, 0, result)
}

func TestLinearCurve(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0
	sensor := Sensor{
		Name:  "sensor",
		Label: "Test",
		Index: 1,
		Input: "test",
		Config: &SensorConfig{
			Id:       "sensor",
			Platform: "platform",
			Index:    1,
		},
		MovingAvg: avgTmp,
	}

	SensorMap[sensor.Config.Id] = &sensor
	config := CurveConfig{
		Id:   "curve",
		Type: LinearCurveType,
		Params: LinearCurveConfig{
			Sensor:  sensor.Config.Id,
			MinTemp: 40,
			MaxTemp: 80,
		},
	}

	// WHEN
	result, err := evaluateCurve(config)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 127, result)
}
