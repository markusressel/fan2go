package internal

import (
	"context"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"time"
)

type SensorMonitor interface {
	Run(ctx context.Context) error
	GetLast() (float64, error)
}

type sensorMonitor struct {
	sensor      Sensor
	pollingRate time.Duration
}

func NewSensorMonitor(sensor Sensor, pollingRate time.Duration) SensorMonitor {
	return sensorMonitor{
		sensor:      sensor,
		pollingRate: pollingRate,
	}
}

func (s sensorMonitor) Run(ctx context.Context) error {
	tick := time.Tick(s.pollingRate)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			err := updateSensor(s.sensor)
			if err != nil {
				ui.Fatal("%v", err)
			}
		}
	}
}

// read the current value of a sensors and append it to the moving window
func updateSensor(s Sensor) (err error) {
	value, err := s.GetValue()
	if err != nil {
		return err
	}

	var n = configuration.CurrentConfig.TempRollingWindowSize
	lastAvg := s.GetMovingAvg()
	newAvg := updateSimpleMovingAvg(lastAvg, n, value)
	s.SetMovingAvg(newAvg)

	return nil
}

// calculates the new moving average, based on an existing average and buffer size
func updateSimpleMovingAvg(oldAvg float64, n int, newValue float64) float64 {
	return oldAvg + (1/float64(n))*(newValue-oldAvg)
}

func (s sensorMonitor) GetLast() (float64, error) {
	return s.sensor.GetValue()
}
