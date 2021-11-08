package sensors

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
)

type HwmonSensor struct {
	Name      string                      `json:"name"`
	Label     string                      `json:"label"`
	Index     int                         `json:"index"`
	Input     string                      `json:"string"`
	Config    *configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                     `json:"moving_avg"`
}

func (sensor HwmonSensor) GetId() string {
	return sensor.Name
}

func (sensor HwmonSensor) GetLabel() string {
	return sensor.Label
}

func (sensor HwmonSensor) GetConfig() *configuration.SensorConfig {
	return sensor.Config
}

func (sensor *HwmonSensor) SetConfig(config *configuration.SensorConfig) {
	sensor.Config = config
}

func (sensor HwmonSensor) GetValue() (result float64, err error) {
	integer, err := util.ReadIntFromFile(sensor.Input)
	if err != nil {
		return 0, err
	}
	result = float64(integer)
	return result, err
}

func (sensor HwmonSensor) GetMovingAvg() (avg float64) {
	return sensor.MovingAvg
}

func (sensor *HwmonSensor) SetMovingAvg(avg float64) {
	sensor.MovingAvg = avg
}
