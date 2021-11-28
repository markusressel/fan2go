package controller

import (
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

var (
	LinearFan = map[int][]float64{
		0:   {0.0},
		255: {255.0},
	}

	NeverStoppingFan = map[int][]float64{
		0:   {50.0},
		50:  {50.0},
		255: {255.0},
	}

	CappedFan = map[int][]float64{
		0:   {0.0},
		1:   {0.0},
		2:   {0.0},
		3:   {0.0},
		4:   {0.0},
		5:   {0.0},
		6:   {20.0},
		200: {200.0},
	}

	CappedNeverStoppingFan = map[int][]float64{
		0:   {50.0},
		50:  {50.0},
		200: {200.0},
	}
)

type mockPersistence struct{}

func (p mockPersistence) SaveFanPwmData(fan fans.Fan) (err error) { return nil }
func (p mockPersistence) LoadFanPwmData(fan fans.Fan) (map[int][]float64, error) {
	fanCurveDataMap := map[int][]float64{}
	return fanCurveDataMap, nil
}

func CreateFan(neverStop bool, curveData map[int][]float64) (fan fans.Fan, err error) {
	configuration.CurrentConfig.RpmRollingWindowSize = 10

	fan = &fans.HwMonFan{
		Config: configuration.FanConfig{
			ID: "fan1",
			HwMon: &configuration.HwMonFanConfig{
				Platform: "platform",
				Index:    1,
			},
			NeverStop: neverStop,
			Curve:     "curve",
		},
		FanCurveData: &map[int]*rolling.PointPolicy{},
		PwmOutput:    "fan1_output",
		RpmInput:     "fan1_rpm",
	}
	fans.FanMap[fan.GetConfig().ID] = fan

	err = fan.AttachFanCurveData(&curveData)

	return fan, err
}

func TestLinearFan(t *testing.T) {
	// GIVEN
	fan, _ := CreateFan(false, LinearFan)

	// WHEN
	startPwm, maxPwm := fans.ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 1, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan, _ := CreateFan(false, NeverStoppingFan)

	// WHEN
	startPwm, maxPwm := fans.ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestCappedFan(t *testing.T) {
	// GIVEN
	fan, _ := testingutils.CreateFan(false, CappedFan)

	// WHEN
	startPwm, maxPwm := fans.ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 6, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCappedNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan, _ := CreateFan(false, CappedNeverStoppingFan)

	// WHEN
	startPwm, maxPwm := fans.ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCalculateTargetSpeedLinear(t *testing.T) {
	// GIVEN
	avgTmp := 50000.0
	s := CreateSensor(
		"sensor",
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    0,
		},
		avgTmp,
	)

	curveConfig := curves.createLinearCurveConfig(
		"curve",
		s.GetConfig().ID,
		40,
		60,
	)
	curve, _ := curves.NewSpeedCurve(curveConfig)

	fan, _ := CreateFan(false, linearFan)

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

	s := internal.createSensor(
		"sensor",
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    0,
		},
		avgTmp,
	)

	curveConfig := curves.createLinearCurveConfig(
		"curve",
		s.GetConfig().ID,
		40,
		60,
	)
	curve, _ := curves.NewSpeedCurve(curveConfig)

	fan, _ := CreateFan(true, cappedFan)

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
