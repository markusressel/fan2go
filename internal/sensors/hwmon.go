package sensors

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
	"sync"
)

type HwmonSensor struct {
	Label     string                     `json:"label"`
	Index     int                        `json:"index"`
	Input     string                     `json:"string"`
	Max       int                        `json:"max"`
	Min       int                        `json:"min"`
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`

	mu sync.Mutex
}

func (sensor *HwmonSensor) GetId() string {
	return sensor.Config.ID
}

func (sensor *HwmonSensor) GetLabel() string {
	return sensor.Label
}

func (sensor *HwmonSensor) GetConfig() configuration.SensorConfig {
	return sensor.Config
}

func (sensor *HwmonSensor) GetValue() (result float64, err error) {
	integer, err := util.ReadIntFromFile(sensor.Input)
	if err != nil {
		return 0, err
	}
	result = float64(integer)
	return result, err
}

func (sensor *HwmonSensor) GetMovingAvg() (avg float64) {
	sensor.mu.Lock()
	defer sensor.mu.Unlock()
	return sensor.MovingAvg
}

func (sensor *HwmonSensor) SetMovingAvg(avg float64) {
	sensor.mu.Lock()
	defer sensor.mu.Unlock()
	sensor.MovingAvg = avg
}
