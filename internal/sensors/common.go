package sensors

import (
	"fmt"
	"sync"

	"github.com/markusressel/fan2go/internal/configuration"
)

type Sensor interface {
	GetId() string

	GetLabel() string

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
		return CreateNvidiaSensor(config)
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

	if config.Disk != nil {
		return &DiskSensor{
			Config: config,
			mu:     sync.Mutex{},
		}, nil
	}

	return nil, fmt.Errorf("no matching sensor type for sensor: %s", config.ID)
}
