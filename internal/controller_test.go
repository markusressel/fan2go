package internal

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

type mockPersistence struct{}

func (p mockPersistence) SaveFanPwmData(fan Fan) (err error) { return nil }
func (p mockPersistence) LoadFanPwmData(fan Fan) (map[int][]float64, error) {
	fanCurveDataMap := map[int][]float64{}
	return fanCurveDataMap, nil
}

func TestLinearFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, linearFan)

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 1, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, neverStoppingFan)

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestCappedFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, cappedFan)

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 6, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCappedNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, cappedNeverStoppingFan)

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCalculateTargetSpeedLinear(t *testing.T) {
	// GIVEN
	avgTmp := 50000.0
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
		60,
	)
	curve, _ := NewSpeedCurve(curveConfig)

	fan, _ := createFan(false, linearFan)

	controller := fanController{
		mockPersistence{},
		fan,
		curve,
		time.Duration(100),
	}
	// WHEN
	optimal, err := controller.calculateOptimalPwm(fan)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 127, optimal)
}

func TestCalculateTargetSpeedNeverStop(t *testing.T) {
	// GIVEN
	avgTmp := 40000.0

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
		60,
	)
	curve, _ := NewSpeedCurve(curveConfig)

	fan, _ := createFan(true, cappedFan)

	controller := fanController{
		mockPersistence{},
		fan,
		curve,
		time.Duration(100),
	}

	// WHEN
	optimal, err := controller.calculateOptimalPwm(fan)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	target := calculateTargetPwm(fan, 0, optimal)

	// THEN
	assert.Equal(t, 0, optimal)
	assert.Greater(t, fan.GetMinPwm(), 0)
	assert.Equal(t, fan.GetMinPwm(), target)
}
