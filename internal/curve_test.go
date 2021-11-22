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
		ID: id,
		Linear: &configuration.LinearCurveConfig{
			Sensor: sensorId,
			Min:    minTemp,
			Max:    maxTemp,
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
		ID: id,
		Linear: &configuration.LinearCurveConfig{
			Sensor: sensorId,
			Steps:  steps,
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
		ID: id,
		Function: &configuration.FunctionCurveConfig{
			Type:   function,
			Curves: curveIds,
		},
	}
	return curve
}

func TestLinearCurveWithMinMax(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0

	s := createSensor(
		"sensor",
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    0,
		},
		avgTmp,
	)

	curveConfig := createLinearCurveConfig(
		"curve",
		s.GetConfig().ID,
		40,
		80,
	)
	curve, err := NewSpeedCurve(curveConfig)

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
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    0,
		},
		avgTmp,
	)

	curveConfig := createLinearCurveConfigWithSteps(
		"curve",
		s.GetConfig().ID,
		map[int]int{
			40: 0,
			50: 30,
			60: 100,
			70: 255,
		},
	)
	curve, err := NewSpeedCurve(curveConfig)

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
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    1,
		},
		temp1,
	)
	sensor2 := createSensor(
		"sensor2",
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    2,
		},
		temp2,
	)

	curve1 := createLinearCurveConfig(
		"case_fan_front",
		sensor1.GetConfig().ID,
		40,
		80,
	)
	NewSpeedCurve(curve1)
	curve2 := createLinearCurveConfig(
		"case_fan_back",
		sensor2.GetConfig().ID,
		40,
		80,
	)
	NewSpeedCurve(curve2)

	function := configuration.FunctionAverage
	functionCurveConfig := createFunctionCurveConfig(
		"avg_function_curve",
		function,
		[]string{
			curve1.ID,
			curve2.ID,
		},
	)
	functionCurve, err := NewSpeedCurve(functionCurveConfig)

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
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    1,
		},
		temp1,
	)
	sensor2 := createSensor(
		"sensor2",
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    2,
		},
		temp2,
	)

	curve1 := createLinearCurveConfig(
		"case_fan_front",
		sensor1.GetConfig().ID,
		40,
		80,
	)
	NewSpeedCurve(curve1)
	curve2 := createLinearCurveConfig(
		"case_fan_back",
		sensor2.GetConfig().ID,
		40,
		80,
	)
	NewSpeedCurve(curve2)

	function := configuration.FunctionMinimum
	functionCurveConfig := createFunctionCurveConfig(
		"max_function_curve",
		function,
		[]string{
			curve1.ID,
			curve2.ID,
		},
	)
	functionCurve, err := NewSpeedCurve(functionCurveConfig)

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
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    1,
		},
		temp1,
	)
	sensor2 := createSensor(
		"sensor2",
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    2,
		},
		temp2,
	)
	curve1 := createLinearCurveConfig(
		"case_fan_front",
		sensor1.GetConfig().ID,
		40,
		80,
	)
	NewSpeedCurve(curve1)
	curve2 := createLinearCurveConfig(
		"case_fan_back",
		sensor2.GetConfig().ID,
		40,
		80,
	)
	NewSpeedCurve(curve2)

	function := configuration.FunctionMaximum
	functionCurveConfig := createFunctionCurveConfig(
		"max_function_curve",
		function,
		[]string{
			curve1.ID,
			curve2.ID,
		},
	)
	functionCurve, err := NewSpeedCurve(functionCurveConfig)

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
