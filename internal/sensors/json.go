package sensors

import (
	"encoding/json"

	"github.com/markusressel/fan2go/internal/configuration"
)

// The sensor monitors update the moving average of a sensor while the REST API serializes it.
// The MarshalJSON methods below serialize a snapshot of the sensor that is taken under its lock.
// Each snapshot type has the same fields and json tags as the sensor type, without the mutex.

type hwmonSensorJson struct {
	Label     string                     `json:"label"`
	Index     int                        `json:"index"`
	Channel   int                        `json:"channel"`
	Input     string                     `json:"string"`
	Max       int                        `json:"max"`
	Min       int                        `json:"min"`
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`
}

func (sensor *HwmonSensor) MarshalJSON() ([]byte, error) {
	sensor.mu.Lock()
	snapshot := hwmonSensorJson{
		Label:     sensor.Label,
		Index:     sensor.Index,
		Channel:   sensor.Channel,
		Input:     sensor.Input,
		Max:       sensor.Max,
		Min:       sensor.Min,
		Config:    sensor.Config,
		MovingAvg: sensor.MovingAvg,
	}
	sensor.mu.Unlock()
	return json.Marshal(snapshot)
}

type fileSensorJson struct {
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`
}

func (sensor *FileSensor) MarshalJSON() ([]byte, error) {
	sensor.mu.Lock()
	snapshot := fileSensorJson{
		Config:    sensor.Config,
		MovingAvg: sensor.MovingAvg,
	}
	sensor.mu.Unlock()
	return json.Marshal(snapshot)
}

type cmdSensorJson struct {
	Name      string                     `json:"name"`
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`
}

func (sensor *CmdSensor) MarshalJSON() ([]byte, error) {
	sensor.mu.Lock()
	snapshot := cmdSensorJson{
		Name:      sensor.Name,
		Config:    sensor.Config,
		MovingAvg: sensor.MovingAvg,
	}
	sensor.mu.Unlock()
	return json.Marshal(snapshot)
}

type diskSensorJson struct {
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`
}

func (s *DiskSensor) MarshalJSON() ([]byte, error) {
	s.mu.Lock()
	snapshot := diskSensorJson{
		Config:    s.Config,
		MovingAvg: s.MovingAvg,
	}
	s.mu.Unlock()
	return json.Marshal(snapshot)
}
