package internal

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestFunctionCurve(t *testing.T) {
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

	function := "average"
	curves := []CurveConfig{
		{
			Id:   "case_fan_front",
			Type: "linear",
			Params: map[string]interface{}{
				"Sensor":  "sensor",
				"MinTemp": 40,
				"MaxTemp": 60,
			},
		},
		{
			Id:   "case_fan_back",
			Type: "linear",
			Params: map[string]interface{}{
				"Sensor":  "sensor",
				"MinTemp": 40,
				"MaxTemp": 60,
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
	assert.Equal(t, 255, result)
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
		Params: map[string]interface{}{
			"Sensor":  sensor.Config.Id,
			"MinTemp": 40,
			"MaxTemp": 80,
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
