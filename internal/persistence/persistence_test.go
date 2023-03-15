package persistence

import (
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
)

const (
	dbTestingPath = "./test.db"
)

var (
	LinearFan = map[int]float64{
		0:   0.0,
		255: 255.0,
	}

	NeverStoppingFan = map[int]float64{
		0:   50.0,
		50:  50.0,
		255: 255.0,
	}
)

func TestPersistence_DeleteFanPwmData(t *testing.T) {
	// GIVEN
	p := NewPersistence(dbTestingPath)
	fan, _ := createFan(false, LinearFan)
	_ = p.SaveFanPwmData(fan)

	// WHEN
	err := p.DeleteFanPwmData(fan)
	assert.NoError(t, err)

	// THEN
	data, err := p.LoadFanPwmData(fan)
	assert.Nil(t, data)
	assert.Error(t, err)
}

func TestPersistence_SaveFanPwmData_LinearFanInterpolated(t *testing.T) {
	// GIVEN
	p := NewPersistence(dbTestingPath)

	expected := util.InterpolateLinearly(&LinearFan, 0, 255)
	fan, _ := createFan(false, expected)

	// WHEN
	err := p.SaveFanPwmData(fan)

	// THEN
	assert.Nil(t, err)
}

func TestPersistence_LoadFanPwmData_LinearFanInterpolated(t *testing.T) {
	// GIVEN
	persistence := NewPersistence(dbTestingPath)

	expected := util.InterpolateLinearly(&LinearFan, 0, 255)
	fan, _ := createFan(false, expected)

	err := persistence.SaveFanPwmData(fan)
	assert.NoError(t, err)

	// WHEN
	fanData, err := persistence.LoadFanPwmData(fan)

	// THEN
	assert.Nil(t, err)
	assert.NotNil(t, fanData)
	assert.Equal(t, expected, fanData)
}

func TestPersistence_SaveFanPwmData_SamplesNotInterpolated(t *testing.T) {
	// GIVEN
	p := NewPersistence(dbTestingPath)

	expected := NeverStoppingFan
	fan, _ := createFan(false, expected)

	// WHEN
	err := p.SaveFanPwmData(fan)

	// THEN
	assert.Nil(t, err)
}

func TestPersistence_LoadFanPwmData_SamplesNotInterpolated(t *testing.T) {
	// GIVEN
	persistence := NewPersistence(dbTestingPath)

	expected := NeverStoppingFan
	fan, _ := createFan(false, expected)

	err := persistence.SaveFanPwmData(fan)
	assert.NoError(t, err)

	// WHEN
	fanData, err := persistence.LoadFanPwmData(fan)

	// THEN
	assert.Nil(t, err)
	assert.NotNil(t, fanData)
	assert.Equal(t, expected, fanData)
}

func createFan(neverStop bool, curveData map[int]float64) (fan fans.Fan, err error) {
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
	}
	fans.FanMap[fan.GetId()] = fan

	err = fan.AttachFanCurveData(&curveData)

	return fan, err
}
