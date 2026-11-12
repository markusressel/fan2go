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

	// RecordReading stores a successful raw reading (written by the sensor monitor), and returns
	// the number of consecutive failed readings before it.
	RecordReading(r Reading) (previousFailures int)
	// RecordFailure counts a failed reading (written by the sensor monitor), and returns the number
	// of consecutive failed readings, including this one.
	RecordFailure() (consecutiveFailures int)
	// GetLastReading returns the last successful raw reading, and false if there is none yet.
	GetLastReading() (Reading, bool)
	// GetConsecutiveFailures returns the number of failed readings since the last successful one.
	GetConsecutiveFailures() int
	// GetReadingStatus returns a consistent snapshot of the recorded readings and failures.
	GetReadingStatus() ReadingStatus
}

func NewSensor(config configuration.SensorConfig) (Sensor, error) {
	if config.HwMon != nil {
		return &HwmonSensor{
			Index:   config.HwMon.Index,
			Channel: config.HwMon.Channel,
			Input:   config.HwMon.TempInput,
			Config:  config,

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
