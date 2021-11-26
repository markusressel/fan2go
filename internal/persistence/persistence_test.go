package persistence

import (
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/stretchr/testify/assert"
	"testing"
)

const (
	dbTestingPath = "./test.db"
)

func TestWriteFan(t *testing.T) {
	// GIVEN
	p := NewPersistence(dbTestingPath)

	fan, _ := createFan(false, linearFan)

	// WHEN
	err := p.SaveFanPwmData(fan)

	// THEN
	assert.Nil(t, err)
}

func TestReadFan(t *testing.T) {
	// GIVEN
	persistence := NewPersistence(dbTestingPath)

	fan, _ := createFan(false, neverStoppingFan)
	err := persistence.SaveFanPwmData(fan)

	fan, _ = createFan(false, linearFan)

	// WHEN
	fanData, err := persistence.LoadFanPwmData(fan)

	// THEN
	assert.Nil(t, err)
	assert.NotNil(t, fanData)
}

func createFan(neverStop bool, curveData map[int][]float64) (fan fans.Fan, err error) {
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

	err = controller.AttachFanCurveData(&curveData, fan)

	return fan, err
}
