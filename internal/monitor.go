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
	// clock is the time source of the ticker and the reading timestamps
	clock util.Clock
}

func NewSensorMonitor(sensor sensors.Sensor, pollingRate time.Duration) SensorMonitor {
	return NewSensorMonitorWithClock(sensor, pollingRate, util.RealClock)
}

// NewSensorMonitorWithClock is NewSensorMonitor with an explicit time source, e.g. a util.FakeClock in tests.
func NewSensorMonitorWithClock(sensor sensors.Sensor, pollingRate time.Duration, clock util.Clock) SensorMonitor {
	return sensorMonitor{
		sensor:      sensor,
		pollingRate: pollingRate,
		clock:       clock,
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

	clock := s.clock
	if clock == nil {
		clock = util.RealClock
	}
	tick := clock.NewTicker(s.pollingRate)
	defer tick.Stop()

	safeUpdateSensor(s.sensor, clock)

	for {
		select {
		case <-ctx.Done():
			ui.Info("Stopping sensor monitor for sensor %s...", s.sensor.GetId())
			return nil
		case <-tick.C():
			safeUpdateSensor(s.sensor, clock)
		}
	}
}

// safeUpdateSensor calls updateSensor, and handles a panic (e.g. of a sensor backend) like a failed reading.
func safeUpdateSensor(s sensors.Sensor, clock util.Clock) {
	defer func() {
		if r := recover(); r != nil {
			ui.Error("Panic while updating sensor %s: %v\n%s", s.GetId(), r, debug.Stack())
			recordSensorFailure(s, fmt.Errorf("panic while updating sensor %s: %v", s.GetId(), r))
		}
	}()
	updateSensor(s, clock)
}

// updateSensor reads the current value of a sensor, records it and updates the moving average with it.
// A failed reading is recorded too, and leaves the moving average unchanged.
// Only the first failed reading and the recovery are logged, not every failed reading.
func updateSensor(s sensors.Sensor, clock util.Clock) {
	value, err := s.GetValue()
	if err != nil {
		recordSensorFailure(s, err)
		return
	}

	_, hadReading := s.GetLastReading()
	if hadReading {
		var n = configuration.CurrentConfig.TempRollingWindowSize
		lastAvg := s.GetMovingAvg()
		newAvg := util.UpdateExponentialMovingAvg(lastAvg, n, value)
		s.SetMovingAvg(newAvg)
	} else {
		// the first successful reading replaces the average, which is meaningless before that
		s.SetMovingAvg(value)
	}

	previousFailures := s.RecordReading(sensors.Reading{Value: value, Time: clock.Now()})
	if previousFailures > 0 {
		ui.Info("Sensor %s: reading succeeded again after %d failed readings", s.GetId(), previousFailures)
	}
}

// recordSensorFailure counts a failed reading of the sensor, and logs it if it is the first one in a row.
func recordSensorFailure(s sensors.Sensor, err error) {
	if s.RecordFailure() == 1 {
		ui.Warning("Error reading sensor %s (further errors are not logged until it recovers): %v", s.GetId(), err)
	}
}
