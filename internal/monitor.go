package internal

import (
	"context"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"time"
)

type SensorMonitor interface {
	Run(ctx context.Context) error
}

type sensorMonitor struct {
	sensor sensors.Sensor
}

func NewSensorMonitor(sensor sensors.Sensor) SensorMonitor {
	return sensorMonitor{
		sensor: sensor,
	}
}

func (s sensorMonitor) Run(ctx context.Context) error {
	currentRate := configuration.CurrentConfig.TempSensorPollingRate
	tick := time.NewTicker(currentRate)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			ui.Info("Stopping sensor monitor for sensor %s...", s.sensor.GetId())
			return nil
		case <-tick.C:
			if newRate := configuration.CurrentConfig.TempSensorPollingRate; newRate != currentRate {
				tick.Reset(newRate)
				currentRate = newRate
			}
			err := updateSensor(s.sensor)
			if err != nil {
				ui.Warning("Error updating sensor: %v", err)
			}
		}
	}
}

// read the current value of a sensors and append it to the moving window
func updateSensor(s sensors.Sensor) (err error) {
	value, err := s.GetValue()
	if err != nil {
		return err
	}

	var n = configuration.CurrentConfig.TempRollingWindowSize
	lastAvg := s.GetMovingAvg()
	newAvg := util.UpdateSimpleMovingAvg(lastAvg, n, value)
	s.SetMovingAvg(newAvg)

	return nil
}
