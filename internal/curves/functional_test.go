package curves

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/stretchr/testify/assert"
	"testing"
)

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

func TestFunctionCurveSum(t *testing.T) {
	// GIVEN
	temp1 := 50000.0
	temp2 := 60000.0

	s1 := &MockSensor{
		ID:        "cpu_sensor",
		Name:      "sensor1",
		MovingAvg: temp1,
	}
	sensors.RegisterSensor(s1)

	s2 := &MockSensor{
		ID:        "mainboard_sensor",
		Name:      "sensor2",
		MovingAvg: temp2,
	}
	sensors.RegisterSensor(s2)

	curve1 := createLinearCurveConfig(
		"case_fan_front1",
		s1.GetId(),
		40,
		80,
	)

	c1, _ := NewSpeedCurve(curve1)
	RegisterSpeedCurve(c1)

	curve2 := createLinearCurveConfig(
		"case_fan_back1",
		s2.GetId(),
		40,
		80,
	)

	var c2 SpeedCurve
	c2, _ = NewSpeedCurve(curve2)
	RegisterSpeedCurve(c2)

	function := configuration.FunctionSum
	functionCurveConfig := createFunctionCurveConfig(
		"sum_function_curve",
		function,
		[]string{
			c1.GetId(),
			c2.GetId(),
		},
	)
	functionCurve, _ := NewSpeedCurve(functionCurveConfig)
	RegisterSpeedCurve(functionCurve)

	// WHEN
	result, err := functionCurve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 63.75+127.5, result)
}

func TestFunctionCurveDifference(t *testing.T) {
	// GIVEN
	temp1 := 60000.0
	temp2 := 50000.0

	s1 := &MockSensor{
		ID:        "cpu_sensor",
		Name:      "sensor1",
		MovingAvg: temp1,
	}
	sensors.RegisterSensor(s1)

	s2 := &MockSensor{
		ID:        "mainboard_sensor",
		Name:      "sensor2",
		MovingAvg: temp2,
	}
	sensors.RegisterSensor(s2)

	curve1 := createLinearCurveConfig(
		"case_fan_front1",
		s1.GetId(),
		40,
		80,
	)
	c1, _ := NewSpeedCurve(curve1)
	RegisterSpeedCurve(c1)

	curve2 := createLinearCurveConfig(
		"case_fan_back1",
		s2.GetId(),
		40,
		80,
	)
	c2, _ := NewSpeedCurve(curve2)
	RegisterSpeedCurve(c2)

	function := configuration.FunctionDifference
	functionCurveConfig := createFunctionCurveConfig(
		"difference_function_curve",
		function,
		[]string{
			c1.GetId(),
			c2.GetId(),
		},
	)
	functionCurve, _ := NewSpeedCurve(functionCurveConfig)
	RegisterSpeedCurve(functionCurve)

	// WHEN
	result, err := functionCurve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 127.5-63.75, result)
}

func TestFunctionCurveAverage(t *testing.T) {
	// GIVEN
	temp1 := 40000.0
	temp2 := 80000.0

	s1 := &MockSensor{
		ID:        "cpu_sensor",
		Name:      "sensor1",
		MovingAvg: temp1,
	}
	sensors.RegisterSensor(s1)

	s2 := &MockSensor{
		ID:        "mainboard_sensor",
		Name:      "sensor2",
		MovingAvg: temp2,
	}
	sensors.RegisterSensor(s2)

	curve1 := createLinearCurveConfig(
		"case_fan_front1",
		s1.GetId(),
		40,
		80,
	)
	c1, _ := NewSpeedCurve(curve1)
	RegisterSpeedCurve(c1)

	curve2 := createLinearCurveConfig(
		"case_fan_back1",
		s2.GetId(),
		40,
		80,
	)
	c2, _ := NewSpeedCurve(curve2)
	RegisterSpeedCurve(c2)

	function := configuration.FunctionAverage
	functionCurveConfig := createFunctionCurveConfig(
		"avg_function_curve",
		function,
		[]string{
			c1.GetId(),
			c2.GetId(),
		},
	)
	functionCurve, _ := NewSpeedCurve(functionCurveConfig)
	RegisterSpeedCurve(functionCurve)

	// WHEN
	result, err := functionCurve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 127.5, result)
}

func TestFunctionCurveDelta(t *testing.T) {
	// GIVEN
	temp1 := 20000.0
	temp2 := 40000.0

	s1 := &MockSensor{
		ID:        "ambient_sensor",
		Name:      "sensor_ambient",
		MovingAvg: temp1,
	}
	sensors.RegisterSensor(s1)

	s2 := &MockSensor{
		ID:        "water_sensor",
		Name:      "sensor_water",
		MovingAvg: temp2,
	}
	sensors.RegisterSensor(s2)

	curve1 := createLinearCurveConfig(
		"case_fan_front2",
		s1.GetId(),
		18,
		60,
	)
	c1, _ := NewSpeedCurve(curve1)
	RegisterSpeedCurve(c1)

	curve2 := createLinearCurveConfig(
		"case_fan_back2",
		s2.GetId(),
		18,
		60,
	)
	c2, _ := NewSpeedCurve(curve2)
	RegisterSpeedCurve(c2)

	function := configuration.FunctionDelta
	functionCurveConfig := createFunctionCurveConfig(
		"delta_function_curve",
		function,
		[]string{
			curve1.ID,
			curve2.ID,
		},
	)
	functionCurve, _ := NewSpeedCurve(functionCurveConfig)
	RegisterSpeedCurve(functionCurve)

	// WHEN
	result, err := functionCurve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	// 20 => ~12.143 ; 40 => ~133.571
	assert.InDeltaf(t, 133.571-12.143, result, 0.001, "The speed difference should be about 121.428")
}

func TestFunctionCurveMinimum(t *testing.T) {
	// GIVEN
	temp1 := 60000.0
	temp2 := 80000.0

	s1 := &MockSensor{
		ID:        "s1",
		Name:      "sensor1",
		MovingAvg: temp1,
	}
	sensors.RegisterSensor(s1)

	s2 := &MockSensor{
		ID:        "s2",
		Name:      "sensor2",
		MovingAvg: temp2,
	}
	sensors.RegisterSensor(s2)

	curve1 := createLinearCurveConfig(
		"case_fan_front3",
		s1.GetId(),
		40,
		80,
	)
	c1, _ := NewSpeedCurve(curve1)
	RegisterSpeedCurve(c1)

	curve2 := createLinearCurveConfig(
		"case_fan_back3",
		s2.GetId(),
		40,
		80,
	)
	c2, _ := NewSpeedCurve(curve2)
	RegisterSpeedCurve(c2)

	function := configuration.FunctionMinimum
	functionCurveConfig := createFunctionCurveConfig(
		"max_function_curve1",
		function,
		[]string{
			curve1.ID,
			curve2.ID,
		},
	)
	functionCurve, _ := NewSpeedCurve(functionCurveConfig)

	// WHEN
	result, err := functionCurve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 127.5, result)
}

func TestFunctionCurveMaximum(t *testing.T) {
	// GIVEN
	temp1 := 40000.0
	temp2 := 80000.0

	s1 := &MockSensor{
		ID:        "s1",
		Name:      "sensor1",
		MovingAvg: temp1,
	}
	sensors.RegisterSensor(s1)

	s2 := &MockSensor{
		ID:        "s1",
		Name:      "sensor2",
		MovingAvg: temp2,
	}
	sensors.RegisterSensor(s2)

	curve1 := createLinearCurveConfig(
		"case_fan_front4",
		s1.GetId(),
		40,
		80,
	)
	c1, _ := NewSpeedCurve(curve1)
	RegisterSpeedCurve(c1)

	curve2 := createLinearCurveConfig(
		"case_fan_back4",
		s2.GetId(),
		40,
		80,
	)
	c2, _ := NewSpeedCurve(curve2)
	RegisterSpeedCurve(c2)

	function := configuration.FunctionMaximum
	functionCurveConfig := createFunctionCurveConfig(
		"max_function_curve2",
		function,
		[]string{
			curve1.ID,
			curve2.ID,
		},
	)
	functionCurve, _ := NewSpeedCurve(functionCurveConfig)

	// WHEN
	result, err := functionCurve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 255.0, result)
}
