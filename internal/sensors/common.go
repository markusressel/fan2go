package sensors

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	cmap "github.com/orcaman/concurrent-map/v2"
)

var (
	SensorMap = cmap.New[Sensor]()
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
		}, nil
	}

	if config.File != nil {
		return &FileSensor{
			Config: config,
		}, nil
	}

	if config.Cmd != nil {
		return &CmdSensor{
			Config: config,
		}, nil
	}

	return nil, fmt.Errorf("no matching sensor type for sensor: %s", config.ID)
}
