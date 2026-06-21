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
	hysteresis configuration.HysteresisConfig,
	steps map[int]float64,
) (curve configuration.CurveConfig) {
	curve = configuration.CurveConfig{
		ID: id,
		Staircase: &configuration.StaircaseCurveConfig{
			Sensor:     sensorId,
			Hysteresis: hysteresis,
			Steps:      steps,
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
		configuration.HysteresisConfig{
			Down: 8,
		},
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
		configuration.HysteresisConfig{
			Down: 8,
		},
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
		configuration.HysteresisConfig{
			Down: 8,
		},
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

func TestStaircaseCurveWithNegativeTemperatures(t *testing.T) {
	// GIVEN
	s := &MockSensor{
		ID:        "neg_sensor",
		Name:      "neg_sensor",
		MovingAvg: -5000.0, // -5°C
	}
	sensors.RegisterSensor(s)

	curveConfig := createStaircaseCurveConfig(
		"curve_neg",
		s.GetId(),
		configuration.HysteresisConfig{
			Down: 3,
		},
		map[int]float64{
			-10: 10,
			10:  50,
		},
	)
	curve, _ := NewSpeedCurve(curveConfig)

	// WHEN
	result, err := curve.Evaluate()
	assert.NoError(t, err)

	// THEN
	assert.Equal(t, 10.0, result) // -5°C matches -10°C threshold

	// WHEN: temperature drops to -12°C, which is within hysteresis threshold of 3 (from -10°C)
	s.MovingAvg = -12000.0
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.Equal(t, 10.0, result) // Holds 10.0 due to hysteresis

	// WHEN: temperature drops to -14°C, which is beyond hysteresis threshold
	s.MovingAvg = -14000.0
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.Equal(t, 0.0, result) // Drops to 0.0
}

func TestStaircaseCurveWithStepAtZero(t *testing.T) {
	// GIVEN
	s := &MockSensor{
		ID:        "zero_sensor",
		Name:      "zero_sensor",
		MovingAvg: 5000.0, // 5°C
	}
	sensors.RegisterSensor(s)

	curveConfig := createStaircaseCurveConfig(
		"curve_zero",
		s.GetId(),
		configuration.HysteresisConfig{
			Down: 5,
		},
		map[int]float64{
			0:  5,
			40: 50,
		},
	)
	curve, _ := NewSpeedCurve(curveConfig)

	// WHEN
	result, err := curve.Evaluate()
	assert.NoError(t, err)

	// THEN
	assert.Equal(t, 5.0, result) // 5°C matches 0°C threshold
}
