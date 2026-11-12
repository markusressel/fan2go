package curves

import (
	"fmt"
	"math"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
)

type SpeedCurve interface {
	GetId() string
	// Evaluate update the value of this SpeedCurve, by calculates a new value based on the current sensor values
	// returns a value in [0..255].
	// It doesn't read the sensors itself, but uses the values recorded by the sensor monitors.
	// On error, it returns the previous value of the curve (see CurrentValue) together with the error,
	// which wraps sensors.ErrSensorStale if a sensor value is stale. Callers must not use that value for control.
	Evaluate() (value float64, err error)
	// CurrentValue returns the current value of the curve, which was calculated by the previous call to Evaluate
	CurrentValue() float64
}

// getSensorReading looks up the sensor with the given id and returns its last successful reading,
// if it is not older than the sensorStaleTimeout at now. Otherwise, the error wraps sensors.ErrSensorStale.
// It never reads the sensor itself: the sensor monitor does that.
func getSensorReading(registry RegistryReader, sensorId string, now time.Time) (sensors.Sensor, sensors.Reading, error) {
	sensor, exists := registry.GetSensor(sensorId)
	if !exists || sensor == nil {
		return nil, sensors.Reading{}, fmt.Errorf("sensor not found with id '%s'", sensorId)
	}
	reading, err := sensors.FreshReading(sensor, now, configuration.CurrentConfig.GetSensorStaleTimeout())
	return sensor, reading, err
}

// getClock returns clock, or util.RealClock if it is nil.
func getClock(clock util.Clock) util.Clock {
	if clock == nil {
		return util.RealClock
	}
	return clock
}

type RegistryReader interface {
	GetSensor(id string) (sensors.Sensor, bool)
	GetCurve(id string) (SpeedCurve, bool)
}

func NewSpeedCurve(config configuration.CurveConfig) (SpeedCurve, error) {
	if config.Linear != nil {
		ret := &LinearSpeedCurve{
			Config: config,
		}
		return ret, nil
	}

	if config.Staircase != nil {
		ret := &StaircaseSpeedCurve{
			Config:   config,
			LastTemp: math.MinInt,
		}
		return ret, nil
	}

	if config.PID != nil {
		pidLoop := util.NewPidLoop(
			config.PID.P,
			config.PID.I,
			config.PID.D,
			0,
			255,
			true,
			false,
		)
		return &PidSpeedCurve{
			Config:  config,
			pidLoop: pidLoop,
		}, nil
	}

	if config.Function != nil {
		return &FunctionSpeedCurve{
			Config: config,
		}, nil
	}

	return nil, fmt.Errorf("no matching curve type for curve: %s", config.ID)
}
