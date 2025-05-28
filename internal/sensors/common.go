package sensors

import (
	"fmt"
	"sync"

	"github.com/markusressel/fan2go/internal/configuration"
	cmap "github.com/orcaman/concurrent-map/v2"
	"github.com/qdm12/reprint"
)

var (
	sensorMap = cmap.New[Sensor]()
)

type Sensor interface {
	GetId() string

	GetConfig() configuration.SensorConfig

	// GetValue returns the current value of this sensor
	GetValue() (float64, error)

	// GetMovingAvg returns the moving average of this sensor's value
	GetMovingAvg() float64
	SetMovingAvg(avg float64)
}

func NewSensor(config configuration.SensorConfig) (Sensor, error) {
	if config.HwMon != nil {
		return &HwmonSensor{
			Index:  config.HwMon.Index,
			Input:  config.HwMon.TempInput,
			Config: config,

			mu: sync.Mutex{},
		}, nil
	}

	if config.Nvidia != nil {
		return &NvidiaSensor{
			Index:  0, // currently nvml only supports one temp sensor/device
			Config: config,

			mu: sync.Mutex{},
			// TODO: nvml.Device?
		}, nil
	}

	if config.File != nil {
		return &FileSensor{
			Config: config,

			mu: sync.Mutex{},
		}, nil
	}

	if config.Cmd != nil {
		return &CmdSensor{
			Config: config,

			mu: sync.Mutex{},
		}, nil
	}

	return nil, fmt.Errorf("no matching sensor type for sensor: %s", config.ID)
}

// RegisterSensor registers a new sensor
func RegisterSensor(sensor Sensor) {
	sensorMap.Set(sensor.GetId(), sensor)
}

// GetSensor returns the sensor with the given id
func GetSensor(id string) (Sensor, bool) {
	return sensorMap.Get(id)
}

// SnapshotSensorMap returns a snapshot of the current sensor map
func SnapshotSensorMap() map[string]Sensor {
	return reprint.This(sensorMap.Items()).(map[string]Sensor)
}
