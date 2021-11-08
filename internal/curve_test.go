package internal

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/stretchr/testify/assert"
	"testing"
)

// helper function to create a linear curve configuration
func createLinearCurveConfig(
	id string,
	sensorId string,
	minTemp int,
	maxTemp int,
) configuration.CurveConfig {
	return configuration.CurveConfig{
		Id:   id,
		Type: configuration.LinearCurveType,
		Params: map[string]interface{}{
			"Sensor":  sensorId,
			"MinTemp": minTemp,
			"MaxTemp": maxTemp,
		},
	}
}

// helper function to create a linear curve configuration with steps
func createLinearCurveConfigWithSteps(
	id string,
	sensorId string,
	steps map[int]int,
) configuration.CurveConfig {
	return configuration.CurveConfig{
		Id:   id,
		Type: configuration.LinearCurveType,
		Params: map[string]interface{}{
			"Sensor": sensorId,
			"Steps":  steps,
		},
	}
}

// helper function to create a function curve configuration
func createFunctionCurveConfig(
	id string,
	function string,
	curveIds []string,
) configuration.CurveConfig {
	return configuration.CurveConfig{
		Id:   id,
		Type: configuration.FunctionCurveType,
		Params: map[string]interface{}{
			"function": function,
			"curves":   curveIds,
		},
	}
}

func TestLinearCurveWithMinMax(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0
	s := sensors.HwmonSensor{
		Name:  "sensor",
		Label: "Test",
		Index: 1,
		Input: "test",
		Config: &configuration.SensorConfig{
			Id:       "sensor",
			Platform: "platform",
			Index:    1,
		},
		MovingAvg: avgTmp,
	}
	SensorMap[s.Config.Id] = &s

	curveConfig := createLinearCurveConfig(
		"curve",
		s.Config.Id,
		40,
		80,
	)

	// WHEN
	result, err := evaluateCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 127, result)
}

func TestLinearCurveWithSteps(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0
	s := sensors.HwmonSensor{
		Name:  "sensor",
		Label: "Test",
		Index: 1,
		Input: "test",
		Config: &configuration.SensorConfig{
			Id:       "sensor",
			Platform: "platform",
			Index:    1,
		},
		MovingAvg: avgTmp,
	}
	SensorMap[s.Config.Id] = &s

	curveConfig := createLinearCurveConfigWithSteps(
		"curve",
		s.Config.Id,
		map[int]int{
			40: 0,
			50: 30,
			60: 100,
			70: 255,
		},
	)

	// WHEN
	result, err := evaluateCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 100, result)
}

func TestFunctionCurveAverage(t *testing.T) {
	// GIVEN
	temp1 := 40000.0
	temp2 := 80000.0
	sensor1 := sensors.HwmonSensor{
		Name:  "sensor1",
		Label: "Test",
		Index: 1,
		Input: "test1",
		Config: &configuration.SensorConfig{
			Id:       "sensor1",
			Platform: "platform",
			Index:    1,
		},
		MovingAvg: temp1,
	}
	sensor2 := sensors.HwmonSensor{
		Name:  "sensor2",
		Label: "Test2",
		Index: 1,
		Input: "test2",
		Config: &configuration.SensorConfig{
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

	function := configuration.FunctionAverage
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
	sensor1 := sensors.HwmonSensor{
		Name:  "sensor1",
		Label: "Test",
		Index: 1,
		Input: "test",
		Config: &configuration.SensorConfig{
			Id:       "sensor1",
			Platform: "platform",
			Index:    1,
		},
		MovingAvg: temp1,
	}
	sensor2 := sensors.HwmonSensor{
		Name:  "sensor2",
		Label: "Test2",
		Index: 1,
		Input: "test2",
		Config: &configuration.SensorConfig{
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

	function := configuration.FunctionMinimum
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
	sensor1 := sensors.HwmonSensor{
		Name:  "sensor1",
		Label: "Test",
		Index: 1,
		Input: "test",
		Config: &configuration.SensorConfig{
			Id:       "sensor1",
			Platform: "platform",
			Index:    1,
		},
		MovingAvg: temp1,
	}
	sensor2 := sensors.HwmonSensor{
		Name:  "sensor2",
		Label: "Test2",
		Index: 1,
		Input: "test2",
		Config: &configuration.SensorConfig{
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

	function := configuration.FunctionMaximum
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

func TestCalculateInterpolatedCurveValue(t *testing.T) {
	// GIVEN
	expectedInputOutput := map[float64]int{
		-100.0: 0.0,
		0:      0,
		100.0:  100.0,
		500.0:  500.0,
		1000.0: 1000.0,
		2000.0: 1000.0,
	}
	steps := map[int]int{
		0:    0,
		100:  100,
		1000: 1000,
	}
	interpolationType := InterpolationTypeLinear

	for input, output := range expectedInputOutput {
		// WHEN
		result := calculateInterpolatedCurveValue(steps, interpolationType, input)

		// THEN
		assert.Equal(t, output, result)
	}
}
