package internal

import (
	"github.com/asecurityteam/rolling"
	"github.com/stretchr/testify/assert"
	"testing"
)

var (
	linearFan = map[int][]float64{
		0:   {0.0},
		255: {255.0},
	}

	neverStoppingFan = map[int][]float64{
		0:   {50.0},
		50:  {50.0},
		255: {255.0},
	}

	cappedFan = map[int][]float64{
		0:   {0.0},
		200: {200.0},
	}

	cappedNeverStoppingFan = map[int][]float64{
		0:   {50.0},
		50:  {50.0},
		200: {200.0},
	}
)

func createFan(curveData map[int][]float64) *Fan {
	CurrentConfig.RpmRollingWindowSize = 10

	fan := Fan{
		Config: &FanConfig{
			Id:        "fan1",
			Platform:  "platform",
			Fan:       1,
			NeverStop: false,
			Sensor:    "sensor",
		},
		FanCurveData: &map[int]*rolling.PointPolicy{},
	}

	AttachFanCurveData(&curveData, &fan)

	return &fan
}

func TestLinearFan(t *testing.T) {
	// GIVEN
	fan := createFan(linearFan)

	// WHEN
	startPwm, maxPwm := GetPwmBoundaries(fan)

	// THEN
	assert.Equal(t, 1, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan := createFan(neverStoppingFan)

	// WHEN
	startPwm, maxPwm := GetPwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestCappedFan(t *testing.T) {
	// GIVEN
	fan := createFan(cappedFan)

	// WHEN
	startPwm, maxPwm := GetPwmBoundaries(fan)

	// THEN
	assert.Equal(t, 1, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCappedNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan := createFan(cappedNeverStoppingFan)

	// WHEN
	startPwm, maxPwm := GetPwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCalculateTargetSpeed(t *testing.T) {
	// GIVEN
	avgTmp := 50000.0
	SensorMap["sensor"] = &Sensor{
		Config: &SensorConfig{
			Min: 0,
			Max: 100,
		},
		MovingAvg: avgTmp,
	}

	fan := createFan(linearFan)

	// WHEN
	target := calculateTargetSpeed(fan)

	// THEN
	assert.Equal(t, 127, target)
}
