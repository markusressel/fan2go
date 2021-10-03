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
		1:   {0.0},
		2:   {0.0},
		3:   {0.0},
		4:   {0.0},
		5:   {0.0},
		6:   {20.0},
		200: {200.0},
	}

	cappedNeverStoppingFan = map[int][]float64{
		0:   {50.0},
		50:  {50.0},
		200: {200.0},
	}
)

func createFan(neverStop bool, curveData map[int][]float64) (fan *Fan, err error) {
	CurrentConfig.RpmRollingWindowSize = 10

	fan = &Fan{
		Config: &FanConfig{
			Id:        "fan1",
			Platform:  "platform",
			Fan:       1,
			NeverStop: neverStop,
			Sensor:    "sensor",
		},
		FanCurveData: &map[int]*rolling.PointPolicy{},
		PwmOutput:    "fan1_output",
		RpmInput:     "fan1_rpm",
	}

	err = AttachFanCurveData(&curveData, fan)

	return fan, err
}

func TestLinearFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, linearFan)

	// WHEN
	startPwm, maxPwm := GetPwmBoundaries(fan)

	// THEN
	assert.Equal(t, 1, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, neverStoppingFan)

	// WHEN
	startPwm, maxPwm := GetPwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestCappedFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, cappedFan)

	// WHEN
	startPwm, maxPwm := GetPwmBoundaries(fan)

	// THEN
	assert.Equal(t, 6, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCappedNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, cappedNeverStoppingFan)

	// WHEN
	startPwm, maxPwm := GetPwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCalculateTargetSpeedLinear(t *testing.T) {
	// GIVEN
	avgTmp := 50000.0
	SensorMap["sensor"] = &Sensor{
		Config: &SensorConfig{
			Min: 0,
			Max: 100,
		},
		MovingAvg: avgTmp,
	}

	fan, _ := createFan(false, linearFan)

	// WHEN
	optimal := calculateOptimalPwm(fan)

	// THEN
	assert.Equal(t, 127, optimal)
}

func TestCalculateTargetSpeedNeverStop(t *testing.T) {
	// GIVEN
	avgTmp := 50000.0
	SensorMap["sensor"] = &Sensor{
		Config: &SensorConfig{
			Min: 50,
			Max: 100,
		},
		MovingAvg: avgTmp,
	}

	fan, _ := createFan(true, cappedFan)

	// WHEN
	optimal := calculateOptimalPwm(fan)
	target := calculateTargetPwm(fan, 0, optimal)

	// THEN
	assert.Equal(t, 0, optimal)
	assert.Equal(t, fan.MinPwm, target)
}
