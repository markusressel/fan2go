package internal

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"testing"
)

// helper function to create a linear curve configuration
func createLinearCurveConfig(
	id string,
	sensorId string,
	minTemp int,
	maxTemp int,
) (curve configuration.CurveConfig) {
	curve = configuration.CurveConfig{
		Id:   id,
		Type: configuration.LinearCurveType,
		Params: map[string]interface{}{
			"Sensor": sensorId,
			"Min":    minTemp,
			"Max":    maxTemp,
		},
	}
	return curve
}

// helper function to create a linear curve configuration with steps
func createLinearCurveConfigWithSteps(
	id string,
	sensorId string,
	steps map[int]int,
) (curve configuration.CurveConfig) {
	curve = configuration.CurveConfig{
		Id:   id,
		Type: configuration.LinearCurveType,
		Params: map[string]interface{}{
			"Sensor": sensorId,
			"Steps":  steps,
		},
	}
	return curve
}

// helper function to create a function curve configuration
func createFunctionCurveConfig(
	id string,
	function string,
	curveIds []string,
) (curve configuration.CurveConfig) {
	curve = configuration.CurveConfig{
		Id:   id,
		Type: configuration.FunctionCurveType,
		Params: map[string]interface{}{
			"function": function,
			"curves":   curveIds,
		},
	}
	return curve
}

func TestLinearCurveWithMinMax(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0

	s := createSensor(
		"sensor",
		configuration.SensorTypeHwMon,
		map[string]interface{}{
			"platform": "platform",
			"index":    0,
		},
		avgTmp,
	)

	curveConfig := createLinearCurveConfig(
		"curve",
		s.GetConfig().Id,
		40,
		80,
	)
	curve := NewSpeedCurve(curveConfig)

	// WHEN
	result, err := curve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 127, result)
}

func TestLinearCurveWithSteps(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0
	s := createSensor(
		"sensor",
		configuration.SensorTypeHwMon,
		map[string]interface{}{
			"platform": "platform",
			"index":    0,
		},
		avgTmp,
	)

	curveConfig := createLinearCurveConfigWithSteps(
		"curve",
		s.GetConfig().Id,
		map[int]int{
			40: 0,
			50: 30,
			60: 100,
			70: 255,
		},
	)
	curve := NewSpeedCurve(curveConfig)

	// WHEN
	result, err := curve.Evaluate()
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
	sensor1 := createSensor(
		"sensor1",
		configuration.SensorTypeHwMon,
		map[string]interface{}{
			"platform": "platform",
			"index":    1,
		},
		temp1,
	)
	sensor2 := createSensor(
		"sensor2",
		configuration.SensorTypeHwMon,
		map[string]interface{}{
			"platform": "platform",
			"index":    2,
		},
		temp2,
	)

	curve1 := createLinearCurveConfig(
		"case_fan_front",
		sensor1.GetConfig().Id,
		40,
		80,
	)
	NewSpeedCurve(curve1)
	curve2 := createLinearCurveConfig(
		"case_fan_back",
		sensor2.GetConfig().Id,
		40,
		80,
	)
	NewSpeedCurve(curve2)

	function := configuration.FunctionAverage
	functionCurveConfig := createFunctionCurveConfig(
		"avg_function_curve",
		function,
		[]string{
			curve1.Id,
			curve2.Id,
		},
	)
	functionCurve := NewSpeedCurve(functionCurveConfig)

	// WHEN
	result, err := functionCurve.Evaluate()
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
	sensor1 := createSensor(
		"sensor1",
		configuration.SensorTypeHwMon,
		map[string]interface{}{
			"platform": "platform",
			"index":    1,
		},
		temp1,
	)
	sensor2 := createSensor(
		"sensor2",
		configuration.SensorTypeHwMon,
		map[string]interface{}{
			"platform": "platform",
			"index":    2,
		},
		temp2,
	)

	curve1 := createLinearCurveConfig(
		"case_fan_front",
		sensor1.GetConfig().Id,
		40,
		80,
	)
	NewSpeedCurve(curve1)
	curve2 := createLinearCurveConfig(
		"case_fan_back",
		sensor2.GetConfig().Id,
		40,
		80,
	)
	NewSpeedCurve(curve2)

	function := configuration.FunctionMinimum
	functionCurveConfig := createFunctionCurveConfig(
		"max_function_curve",
		function,
		[]string{
			curve1.Id,
			curve2.Id,
		},
	)
	functionCurve := NewSpeedCurve(functionCurveConfig)

	// WHEN
	result, err := functionCurve.Evaluate()
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
	sensor1 := createSensor(
		"sensor1",
		configuration.SensorTypeHwMon,
		map[string]interface{}{
			"platform": "platform",
			"index":    1,
		},
		temp1,
	)
	sensor2 := createSensor(
		"sensor2",
		configuration.SensorTypeHwMon,
		map[string]interface{}{
			"platform": "platform",
			"index":    2,
		},
		temp2,
	)
	curve1 := createLinearCurveConfig(
		"case_fan_front",
		sensor1.GetConfig().Id,
		40,
		80,
	)
	NewSpeedCurve(curve1)
	curve2 := createLinearCurveConfig(
		"case_fan_back",
		sensor2.GetConfig().Id,
		40,
		80,
	)
	NewSpeedCurve(curve2)

	function := configuration.FunctionMaximum
	functionCurveConfig := createFunctionCurveConfig(
		"max_function_curve",
		function,
		[]string{
			curve1.Id,
			curve2.Id,
		},
	)
	functionCurve := NewSpeedCurve(functionCurveConfig)

	// WHEN
	result, err := functionCurve.Evaluate()
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
