package curves

import (
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/stretchr/testify/assert"
)

// helper function to create a staircase curve configuration
func createStaircaseCurveConfig(
	id string,
	sensorId string,
	threshold int,
	steps map[int]float64,
) (curve configuration.CurveConfig) {
	curve = configuration.CurveConfig{
		ID: id,
		Staircase: &configuration.StaircaseCurveConfig{
			Sensor:    sensorId,
			Threshold: threshold,
			Steps:     steps,
		},
	}
	return curve
}

func TestStaircaseCurveWithStepsAtMin(t *testing.T) {
	// GIVEN
	avgTmp := 40000.0
	s := &MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.RegisterSensor(s)

	curveConfig := createStaircaseCurveConfig(
		"curve",
		s.GetId(),
		8,
		map[int]float64{
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
	assert.Equal(t, 0.0, result)
}

func TestStaircaseCurveWithStepsInMiddle(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0
	s := &MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.RegisterSensor(s)

	curveConfig := createStaircaseCurveConfig(
		"curve",
		s.GetId(),
		8,
		map[int]float64{
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
	assert.Equal(t, 100.0, result)

	// WHEN
	s.MovingAvg = 55000.0
	result, err = curve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 100.0, result)

	// WHEN
	s.MovingAvg = 52000.0
	result, err = curve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 30.0, result)

}

func TestStaircaseCurveWithStepsAtMax(t *testing.T) {
	// GIVEN
	avgTmp := 70000.0
	s := &MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.RegisterSensor(s)

	curveConfig := createStaircaseCurveConfig(
		"curve",
		s.GetId(),
		8,
		map[int]float64{
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
	assert.Equal(t, 255.0, result)
}
