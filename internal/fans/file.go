package fans

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
)

type FileFan struct {
	Name      string                   `json:"name"`
	Label     string                   `json:"label"`
	FilePath  string                   `json:"string"`
	Config    *configuration.FanConfig `json:"configuration"`
	MovingAvg float64                  `json:"moving_avg"`
}

func (fan FileFan) GetId() string {
	return fan.Name
}

func (fan FileFan) GetLabel() string {
	return fan.Label
}

func (fan FileFan) GetConfig() *configuration.FanConfig {
	return fan.Config
}

func (fan *FileFan) SetConfig(config *configuration.FanConfig) {
	fan.Config = config
}

func (fan FileFan) GetValue() (result float64, err error) {
	integer, err := util.ReadIntFromFile(fan.FilePath)
	if err != nil {
		return MinPwmValue, err
	}
	result = float64(integer)
	return result, err
}

func (fan FileFan) GetMovingAvg() (avg float64) {
	return fan.MovingAvg
}

func (fan *FileFan) SetMovingAvg(avg float64) {
	fan.MovingAvg = avg
}
