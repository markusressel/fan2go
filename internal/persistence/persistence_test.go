package persistence

import (
	"os"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
)

const (
	dbTestingPath = "../testing/test.db"
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

func TestMain(m *testing.M) {
	beforeEach()
	code := m.Run()
	afterEach()
	os.Exit(code)
}

func beforeEach() {
	err := NewPersistence(dbTestingPath).Init()
	if err != nil {
		panic(err)
	}
}

func afterEach() {
	defer func() {
		_ = os.Remove(dbTestingPath)
	}()
}

func TestPersistence_DeleteFanPwmData(t *testing.T) {
	// GIVEN
	p := NewPersistence(dbTestingPath)
	fan, _ := createFan(false, LinearFan)
	_ = p.SaveFanRpmData(fan)

	// WHEN
	err := p.DeleteFanRpmData(fan)
	assert.NoError(t, err)

	// THEN
	data, err := p.LoadFanRpmData(fan)
	assert.Nil(t, data)
	assert.Error(t, err)
}

func TestPersistence_SaveFanPwmData_LinearFanInterpolated(t *testing.T) {
	// GIVEN
	p := NewPersistence(dbTestingPath)

	expected, _ := util.InterpolateLinearly(&LinearFan, 0, 255)
	fan, _ := createFan(false, expected)

	// WHEN
	err := p.SaveFanRpmData(fan)

	// THEN
	assert.Nil(t, err)
}

func TestPersistence_LoadFanPwmData_LinearFanInterpolated(t *testing.T) {
	// GIVEN
	persistence := NewPersistence(dbTestingPath)

	expected, _ := util.InterpolateLinearly(&LinearFan, 0, 255)
	fan, _ := createFan(false, expected)

	err := persistence.SaveFanRpmData(fan)
	assert.NoError(t, err)

	// WHEN
	fanData, err := persistence.LoadFanRpmData(fan)

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
	err := p.SaveFanRpmData(fan)

	// THEN
	assert.Nil(t, err)
}

func TestPersistence_LoadFanPwmData_SamplesNotInterpolated(t *testing.T) {
	// GIVEN
	persistence := NewPersistence(dbTestingPath)

	expected := NeverStoppingFan
	fan, _ := createFan(false, expected)

	err := persistence.SaveFanRpmData(fan)
	assert.NoError(t, err)

	// WHEN
	fanData, err := persistence.LoadFanRpmData(fan)

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
	fans.RegisterFan(fan)

	err = fan.AttachFanRpmCurveData(&curveData)

	return fan, err
}
