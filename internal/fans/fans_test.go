package fans

import (
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/configuration"
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

func CreateFan(neverStop bool, curveData map[int][]float64) (fan Fan, err error) {
	configuration.CurrentConfig.RpmRollingWindowSize = 10

	fan = &HwMonFan{
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
	FanMap[fan.GetId()] = fan

	err = fan.AttachFanCurveData(&curveData)

	return fan, err
}
