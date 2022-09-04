package sensors

import (
	"github.com/markusressel/fan2go/internal/configuration"
)

type VirtualSensor struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

func (sensor VirtualSensor) GetId() string {
	return sensor.Name
}

func (sensor VirtualSensor) GetConfig() configuration.SensorConfig {
	return configuration.SensorConfig{}
}

func (sensor VirtualSensor) GetValue() (float64, error) {
	return sensor.Value, nil
}

func (sensor VirtualSensor) GetMovingAvg() (avg float64) {
	return sensor.Value
}

func (sensor *VirtualSensor) SetMovingAvg(avg float64) {
	sensor.Value = avg
}
