package internal

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

// helper function to create a linear curve configuration
func createLinearCurveConfig(
	id string,
	sensorId string,
	minTemp int,
	maxTemp int,
) CurveConfig {
	return CurveConfig{
		Id:   id,
		Type: LinearCurveType,
		Params: map[string]interface{}{
			"Sensor":  sensorId,
			"MinTemp": minTemp,
			"MaxTemp": maxTemp,
		},
	}
}

// helper function to create a function curve configuration
func createFunctionCurveConfig(
	id string,
	function string,
	curveIds []string,
) CurveConfig {
	return CurveConfig{
		Id:   id,
		Type: FunctionCurveType,
		Params: map[string]interface{}{
			"function": function,
			"curves":   curveIds,
		},
	}
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

	config := createLinearCurveConfig(
		"curve",
		sensor.Config.Id,
		40,
		80,
	)

	// WHEN
	result, err := evaluateCurve(config)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 127, result)
}

func TestFunctionCurveAverage(t *testing.T) {
	// GIVEN
	temp1 := 40000.0
	temp2 := 80000.0
	sensor1 := Sensor{
		Name:  "sensor1",
		Label: "Test",
		Index: 1,
		Input: "test1",
		Config: &SensorConfig{
			Id:       "sensor1",
			Platform: "platform",
			Index:    1,
		},
		MovingAvg: temp1,
	}
	sensor2 := Sensor{
		Name:  "sensor2",
		Label: "Test2",
		Index: 1,
		Input: "test2",
		Config: &SensorConfig{
			Id:       "sensor2",
			Platform: "platform",
			Index:    2,
		},
		MovingAvg: temp2,
	}

	SensorMap[sensor1.Config.Id] = &sensor1
	SensorMap[sensor2.Config.Id] = &sensor2

	curve1 := createLinearCurveConfig(
		"case_fan_front",
		sensor1.Config.Id,
		40,
		80,
	)
	curve2 := createLinearCurveConfig(
		"case_fan_back",
		sensor2.Config.Id,
		40,
		80,
	)

	CurveMap[curve1.Id] = &curve1
	CurveMap[curve2.Id] = &curve2

	function := FunctionAverage
	functionCurve := createFunctionCurveConfig(
		"avg_function_curve",
		function,
		[]string{
			curve1.Id,
			curve2.Id,
		},
	)

	// WHEN
	result, err := evaluateCurve(functionCurve)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 127, result)
}

func TestFunctionCurveMinimum(t *testing.T) {
	// GIVEN
	temp1 := 40000.0
	temp2 := 80000.0
	sensor1 := Sensor{
		Name:  "sensor1",
		Label: "Test",
		Index: 1,
		Input: "test",
		Config: &SensorConfig{
			Id:       "sensor1",
			Platform: "platform",
			Index:    1,
		},
		MovingAvg: temp1,
	}
	sensor2 := Sensor{
		Name:  "sensor2",
		Label: "Test2",
		Index: 1,
		Input: "test2",
		Config: &SensorConfig{
			Id:       "sensor2",
			Platform: "platform",
			Index:    2,
		},
		MovingAvg: temp2,
	}

	SensorMap[sensor1.Config.Id] = &sensor1
	SensorMap[sensor2.Config.Id] = &sensor2

	curve1 := createLinearCurveConfig(
		"case_fan_front",
		sensor1.Config.Id,
		40,
		80,
	)
	curve2 := createLinearCurveConfig(
		"case_fan_back",
		sensor2.Config.Id,
		40,
		80,
	)

	CurveMap[curve1.Id] = &curve1
	CurveMap[curve2.Id] = &curve2

	function := FunctionMinimum
	functionCurve := createFunctionCurveConfig(
		"max_function_curve",
		function,
		[]string{
			curve1.Id,
			curve2.Id,
		},
	)

	// WHEN
	result, err := evaluateCurve(functionCurve)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 0, result)
}

func TestFunctionCurveMaximum(t *testing.T) {
	// GIVEN
	temp1 := 40000.0
	temp2 := 80000.0
	sensor1 := Sensor{
		Name:  "sensor1",
		Label: "Test",
		Index: 1,
		Input: "test",
		Config: &SensorConfig{
			Id:       "sensor1",
			Platform: "platform",
			Index:    1,
		},
		MovingAvg: temp1,
	}
	sensor2 := Sensor{
		Name:  "sensor2",
		Label: "Test2",
		Index: 1,
		Input: "test2",
		Config: &SensorConfig{
			Id:       "sensor2",
			Platform: "platform",
			Index:    2,
		},
		MovingAvg: temp2,
	}

	SensorMap[sensor1.Config.Id] = &sensor1
	SensorMap[sensor2.Config.Id] = &sensor2

	curve1 := createLinearCurveConfig(
		"case_fan_front",
		sensor1.Config.Id,
		40,
		80,
	)
	curve2 := createLinearCurveConfig(
		"case_fan_back",
		sensor2.Config.Id,
		40,
		80,
	)

	CurveMap[curve1.Id] = &curve1
	CurveMap[curve2.Id] = &curve2

	function := FunctionMaximum
	functionCurve := createFunctionCurveConfig(
		"max_function_curve",
		function,
		[]string{
			curve1.Id,
			curve2.Id,
		},
	)

	// WHEN
	result, err := evaluateCurve(functionCurve)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 255, result)
}
