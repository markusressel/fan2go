package curves

import (
	"github.com/markusressel/fan2go/internal/configuration"
)

type MockSensor struct {
	ID        string
	Name      string
	MovingAvg float64
}

func (sensor MockSensor) GetId() string {
	return sensor.ID
}

func (sensor MockSensor) GetLabel() string {
	return sensor.Name
}

func (sensor MockSensor) GetConfig() configuration.SensorConfig {
	panic("not implemented")
}

func (sensor MockSensor) GetValue() (result float64, err error) {
	return sensor.MovingAvg, nil
}

func (sensor MockSensor) GetMovingAvg() (avg float64) {
	return sensor.MovingAvg
}

func (sensor *MockSensor) SetMovingAvg(avg float64) {
	sensor.MovingAvg = avg
}
