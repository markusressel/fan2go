package internal

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type SensorMonitor interface {
	Run(ctx context.Context) error
}

type sensorMonitor struct {
	sensor      sensors.Sensor
	pollingRate time.Duration
}

func NewSensorMonitor(sensor sensors.Sensor, pollingRate time.Duration) SensorMonitor {
	return sensorMonitor{
		sensor:      sensor,
		pollingRate: pollingRate,
	}
}

// Run updates the sensor every pollingRate until ctx is done.
// A panic while updating the sensor is logged like an error, and the monitor keeps running.
// Any other panic stops the monitor and is returned as an error.
func (s sensorMonitor) Run(ctx context.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			ui.Error("Panic in sensor monitor for sensor %s: %v\n%s", s.sensor.GetId(), r, debug.Stack())
			err = fmt.Errorf("panic in sensor monitor for sensor %s: %v", s.sensor.GetId(), r)
		}
	}()

	tick := time.NewTicker(s.pollingRate)
	defer tick.Stop()

	err = safeUpdateSensor(s.sensor)
	if err != nil {
		ui.Warning("Error updating sensor: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			ui.Info("Stopping sensor monitor for sensor %s...", s.sensor.GetId())
			return nil
		case <-tick.C:
			err := safeUpdateSensor(s.sensor)
			if err != nil {
				ui.Warning("Error updating sensor: %v", err)
			}
		}
	}
}

// safeUpdateSensor calls updateSensor, and returns a panic (e.g. of a sensor backend) as an error.
func safeUpdateSensor(s sensors.Sensor) (err error) {
	defer func() {
		if r := recover(); r != nil {
			ui.Error("Panic while updating sensor %s: %v\n%s", s.GetId(), r, debug.Stack())
			err = fmt.Errorf("panic while updating sensor %s: %v", s.GetId(), r)
		}
	}()
	return updateSensor(s)
}

// read the current value of a sensors and append it to the moving window
func updateSensor(s sensors.Sensor) (err error) {
	value, err := s.GetValue()
	if err != nil {
		return err
	}

	var n = configuration.CurrentConfig.TempRollingWindowSize
	lastAvg := s.GetMovingAvg()
	newAvg := util.UpdateExponentialMovingAvg(lastAvg, n, value)
	s.SetMovingAvg(newAvg)

	return nil
}
