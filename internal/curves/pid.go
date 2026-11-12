package curves

import (
	"fmt"
	"sync"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type PidSpeedCurve struct {
	Config   configuration.CurveConfig `json:"config"`
	Value    float64                   `json:"value"`
	registry RegistryReader

	pidLoop *util.PidLoop
	// clock is the time source for the staleness check of the sensor value (nil means util.RealClock)
	clock util.Clock

	// stepMu serializes Evaluate, which steps pidLoop and updates lastStepReadingTime
	stepMu sync.Mutex
	// lastStepReadingTime is the time of the sensor reading the pid loop was last stepped with
	// (zero before the first step)
	lastStepReadingTime time.Time

	// last is the error and time of the last evaluation (guarded by mu, like Value)
	last evaluation

	mu sync.RWMutex
}

func (c *PidSpeedCurve) BindRegistry(registry RegistryReader) {
	c.registry = registry
}

// SetClock replaces the time source for the staleness check of the sensor value. Intended for tests.
func (c *PidSpeedCurve) SetClock(clock util.Clock) {
	c.clock = clock
}

func (c *PidSpeedCurve) GetId() string {
	return c.Config.ID
}

// Evaluate steps the pid loop with the last successful raw reading of the sensor (not its moving average),
// using the time between two readings as dt. It doesn't read the sensor itself.
// The loop is only stepped when a new reading has arrived since the last step. Otherwise, the previous
// value is returned unchanged, so evaluating the curve more often than the sensor is read doesn't distort
// the I and D terms.
// If the time since the reading of the last step is longer than the sensorStaleTimeout (the sensor value
// was stale in between), the loop starts over.
func (c *PidSpeedCurve) Evaluate() (value float64, err error) {
	now := getClock(c.clock).Now()
	c.stepMu.Lock()
	defer c.stepMu.Unlock()
	value, err = c.evaluate(now)
	return c.storeResult(value, err, now)
}

// evaluate steps the pid loop (see Evaluate). The caller must hold stepMu.
func (c *PidSpeedCurve) evaluate(now time.Time) (value float64, err error) {
	if c.registry == nil {
		return 0, fmt.Errorf("no registry bound to speed curve '%s'", c.Config.ID)
	}
	sensor, reading, err := getSensorReading(c.registry, c.Config.PID.Sensor, now)
	if err != nil {
		return 0, fmt.Errorf("curve %s: %w", c.Config.ID, err)
	}

	if !c.lastStepReadingTime.IsZero() && !reading.Time.After(c.lastStepReadingTime) {
		// no new reading since the last step
		return c.CurrentValue(), nil
	}

	if !c.lastStepReadingTime.IsZero() &&
		reading.Time.Sub(c.lastStepReadingTime) > configuration.CurrentConfig.GetSensorStaleTimeout() {
		// the sensor value was stale in between: start over instead of applying the whole outage
		// as a single step (which would make the I term jump)
		c.pidLoop.Reset()
	}

	measured := reading.Value
	pidTarget := c.Config.PID.SetPoint

	loopValue := c.pidLoop.LoopAt(reading.Time, pidTarget, measured/1000.0)
	curveValue := loopValue
	c.lastStepReadingTime = reading.Time

	ui.Debug("Evaluating curve '%s'. Sensor '%s' temp '%.0f°'. Desired speed: %.2f", c.Config.ID, sensor.GetId(), measured/1000, curveValue)
	return curveValue, nil
}

func (c *PidSpeedCurve) storeResult(value float64, err error, now time.Time) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.Value = value
	}
	c.last = evaluation{err: err, at: now}
	return c.Value, err
}

func (c *PidSpeedCurve) SetValue(value float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Value = value
}

func (c *PidSpeedCurve) CurrentValue() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Value
}

func (c *PidSpeedCurve) CurrentResult() CurveResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.last.result(c.Value)
}
