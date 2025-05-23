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

func TestLinearCurveWithMinMax(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0

	s := &MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.RegisterSensor(s)

	curveConfig := createLinearCurveConfig(
		"curve",
		s.GetId(),
		40,
		80,
	)
	curve, _ := NewSpeedCurve(curveConfig)

	// WHEN
	result, err := curve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 127, result)
}

func TestLinearCurveWithStepsAtMin(t *testing.T) {
	// GIVEN
	avgTmp := 40000.0
	s := &MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.RegisterSensor(s)

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
	curve, _ := NewSpeedCurve(curveConfig)

	// WHEN
	result, err := curve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 0, result)
}

func TestLinearCurveWithStepsInMiddle(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0
	s := &MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.RegisterSensor(s)

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
	curve, _ := NewSpeedCurve(curveConfig)

	// WHEN
	result, err := curve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 100, result)
}

func TestLinearCurveWithStepsAtMax(t *testing.T) {
	// GIVEN
	avgTmp := 70000.0
	s := &MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.RegisterSensor(s)

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
	curve, _ := NewSpeedCurve(curveConfig)

	// WHEN
	result, err := curve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 255, result)
}
