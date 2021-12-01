package curves

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

type MockSensor struct {
	ID        string
	Name      string
	MovingAvg float64
}

func (sensor MockSensor) GetId() string {
	return sensor.ID
}

func (sensor MockSensor) GetLabel() string {
	return sensor.Name
}

func (sensor MockSensor) GetConfig() configuration.SensorConfig {
	panic("not implemented")
}

func (sensor MockSensor) GetValue() (result float64, err error) {
	return sensor.MovingAvg, nil
}

func (sensor MockSensor) GetMovingAvg() (avg float64) {
	return sensor.MovingAvg
}

func (sensor *MockSensor) SetMovingAvg(avg float64) {
	sensor.MovingAvg = avg
}

// helper function to create a linear curve configuration with steps
func createLinearCurveConfigWithSteps(
	id string,
	sensorId string,
	steps map[int]float64,
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

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createLinearCurveConfig(
		"curve",
		s.GetId(),
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
	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createLinearCurveConfigWithSteps(
		"curve",
		s.GetId(),
		map[int]float64{
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

	s1 := MockSensor{
		ID:        "cpu_sensor",
		Name:      "sensor1",
		MovingAvg: temp1,
	}
	sensors.SensorMap[s1.GetId()] = &s1

	s2 := MockSensor{
		ID:        "mainboard_sensor",
		Name:      "sensor2",
		MovingAvg: temp2,
	}
	sensors.SensorMap[s2.GetId()] = &s2

	curve1 := createLinearCurveConfig(
		"case_fan_front",
		s1.GetId(),
		40,
		80,
	)
	c1, err := NewSpeedCurve(curve1)
	SpeedCurveMap[c1.GetId()] = c1

	curve2 := createLinearCurveConfig(
		"case_fan_back",
		s2.GetId(),
		40,
		80,
	)
	c2, err := NewSpeedCurve(curve2)
	SpeedCurveMap[c2.GetId()] = c2

	function := configuration.FunctionAverage
	functionCurveConfig := createFunctionCurveConfig(
		"avg_function_curve",
		function,
		[]string{
			c1.GetId(),
			c2.GetId(),
		},
	)
	functionCurve, err := NewSpeedCurve(functionCurveConfig)
	SpeedCurveMap[functionCurve.GetId()] = functionCurve

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

	s1 := MockSensor{
		Name:      "sensor1",
		MovingAvg: temp1,
	}
	sensors.SensorMap[s1.GetId()] = &s1

	s2 := MockSensor{
		Name:      "sensor2",
		MovingAvg: temp2,
	}
	sensors.SensorMap[s2.GetId()] = &s2

	curve1 := createLinearCurveConfig(
		"case_fan_front",
		s1.GetId(),
		40,
		80,
	)
	NewSpeedCurve(curve1)
	curve2 := createLinearCurveConfig(
		"case_fan_back",
		s2.GetId(),
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

	s1 := MockSensor{
		Name:      "sensor1",
		MovingAvg: temp1,
	}
	sensors.SensorMap[s1.GetId()] = &s1

	s2 := MockSensor{
		Name:      "sensor2",
		MovingAvg: temp2,
	}
	sensors.SensorMap[s2.GetId()] = &s2

	curve1 := createLinearCurveConfig(
		"case_fan_front",
		s1.GetId(),
		40,
		80,
	)
	NewSpeedCurve(curve1)
	curve2 := createLinearCurveConfig(
		"case_fan_back",
		s2.GetId(),
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
