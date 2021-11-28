package sensors

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/ui"
)

var (
	SensorMap = map[string]Sensor{}
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

func NewSensor(config configuration.SensorConfig, controllers []*hwmon.HwMonController) (Sensor, error) {
	if config.HwMon != nil {

		for _, controller := range controllers {
			if controller.Platform == config.HwMon.Platform {
				return &HwmonSensor{
					Index:  config.HwMon.Index,
					Input:  controller.TempInputs[config.HwMon.Index-1],
					Config: config,
				}, nil
			}
		}
		ui.Fatal("No hwmon controller found for sensor config: %s", config.ID)
	}

	if config.File != nil {
		return &FileSensor{
			Name:     config.ID,
			FilePath: config.File.Path,
			Config:   config,
		}, nil
	}

	return nil, fmt.Errorf("no matching sensor type for sensor: %s", config.ID)
}
