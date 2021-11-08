package sensors

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
)

type FileSensor struct {
	Name      string                      `json:"name"`
	Label     string                      `json:"label"`
	FilePath  string                      `json:"string"`
	Config    *configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                     `json:"moving_avg"`
}

func (sensor FileSensor) GetId() string {
	return sensor.Name
}

func (sensor FileSensor) GetLabel() string {
	return sensor.Label
}

func (sensor FileSensor) GetConfig() *configuration.SensorConfig {
	return sensor.Config
}

func (sensor *FileSensor) SetConfig(config *configuration.SensorConfig) {
	sensor.Config = config
}

func (sensor FileSensor) GetValue() (result float64, err error) {
	integer, err := util.ReadIntFromFile(sensor.FilePath)
	if err != nil {
		return 0, err
	}
	result = float64(integer)
	return result, err
}

func (sensor FileSensor) GetMovingAvg() (avg float64) {
	return sensor.MovingAvg
}

func (sensor *FileSensor) SetMovingAvg(avg float64) {
	sensor.MovingAvg = avg
}
