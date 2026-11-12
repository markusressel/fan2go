package curves

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
)

// ErrCurveNotEvaluated is the error of the result of a curve that hasn't been evaluated yet.
var ErrCurveNotEvaluated = errors.New("curve has not been evaluated yet")

// CurveResult is the result of the last evaluation of a curve.
type CurveResult struct {
	// Value is the value of the curve in [0..255]. If Err is set, it is the value of the last
	// successful evaluation, which must not be used for control.
	Value float64
	// Err is the error of the last evaluation, nil if it succeeded
	Err error
	// EvaluatedAt is the time of the last evaluation, zero if the curve hasn't been evaluated yet
	EvaluatedAt time.Time
}

type SpeedCurve interface {
	GetId() string
	// Evaluate update the value of this SpeedCurve, by calculates a new value based on the current sensor values
	// returns a value in [0..255], and stores the result (see CurrentResult).
	// It doesn't read the sensors itself, but uses the values recorded by the sensor monitors.
	// On error, it returns the previous value of the curve (see CurrentValue) together with the error,
	// which wraps sensors.ErrSensorStale if a sensor value is stale. Callers must not use that value for control.
	//
	// In the daemon, only the CurveEvaluator calls Evaluate: once per cycle for every curve, in dependency order.
	// Everything else (fan controllers, REST API, statistics) reads the stored result.
	Evaluate() (value float64, err error)
	// CurrentValue returns the current value of the curve, which was calculated by the previous call to Evaluate
	CurrentValue() float64
	// CurrentResult returns the result of the previous call to Evaluate.
	// Before the first call, its Err is ErrCurveNotEvaluated.
	CurrentResult() CurveResult
}

// evaluation is the error and time of the last evaluation of a curve.
// The curves keep it next to their value, guarded by their mutex.
type evaluation struct {
	err error
	at  time.Time
}

// result returns the CurveResult for the given current value of the curve.
func (e evaluation) result(value float64) CurveResult {
	if e.at.IsZero() {
		return CurveResult{Value: value, Err: ErrCurveNotEvaluated}
	}
	return CurveResult{Value: value, Err: e.err, EvaluatedAt: e.at}
}

// resultStore is implemented by the curves of this package. The CurveEvaluator uses it to record
// an evaluation that panicked as a failed result of the curve.
type resultStore interface {
	// storeResult stores the result of an evaluation at now: value (only if err is nil) and err.
	// It returns the value of the curve after that (the previous value if err is set) and err.
	storeResult(value float64, err error, now time.Time) (float64, error)
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
