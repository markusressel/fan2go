package persistence

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"testing"
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

func TestWriteFan(t *testing.T) {
	// GIVEN
	p := NewPersistence(dbTestingPath)

	fan, _ := createFan(false, LinearFan)

	// WHEN
	err := p.SaveFanPwmData(fan)

	// THEN
	assert.Nil(t, err)
}

func TestReadFan(t *testing.T) {
	// GIVEN
	persistence := NewPersistence(dbTestingPath)

	fan, _ := createFan(false, NeverStoppingFan)
	expected := util.InterpolateLinearly(fan.GetFanCurveData(), 0, 255)

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
		PwmOutput: "fan1_output",
		RpmInput:  "fan1_rpm",
	}
	fans.FanMap[fan.GetId()] = fan

	err = fan.AttachFanCurveData(&curveData)

	return fan, err
}
