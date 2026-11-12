package curves

import (
	"fmt"
	"sync"
	"time"

	"github.com/markusressel/fan2go/internal/ui"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
)

type LinearSpeedCurve struct {
	Config   configuration.CurveConfig `json:"config"`
	Value    float64                   `json:"value"`
	registry RegistryReader
	// clock is the time source for the staleness check of the sensor value (nil means util.RealClock)
	clock util.Clock

	// last is the error and time of the last evaluation (guarded by mu, like Value)
	last evaluation

	// hysteresisTemp is the temperature (in milli-degrees) the curve was last evaluated at, with
	// hysteresis.down applied (see applyHysteresis), valid if hasHysteresisTemp (both guarded by mu)
	hysteresisTemp    float64
	hasHysteresisTemp bool

	mu sync.RWMutex
}

// SetClock replaces the time source for the staleness check of the sensor value. Intended for tests.
func (c *LinearSpeedCurve) SetClock(clock util.Clock) {
	c.clock = clock
}

func (c *LinearSpeedCurve) BindRegistry(registry RegistryReader) {
	c.registry = registry
}

func (c *LinearSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c *LinearSpeedCurve) Evaluate() (value float64, err error) {
	now := getClock(c.clock).Now()
	value, err = c.evaluate(now)
	return c.storeResult(value, err, now)
}

func (c *LinearSpeedCurve) evaluate(now time.Time) (value float64, err error) {
	if c.registry == nil {
		return 0, fmt.Errorf("no registry bound to speed curve '%s'", c.Config.ID)
	}
	sensor, _, err := getSensorReading(c.registry, c.Config.Linear.Sensor, now)
	if err != nil {
		return 0, fmt.Errorf("curve %s: %w", c.Config.ID, err)
	}
	var avgTemp = c.applyHysteresis(sensor.GetMovingAvg())

	steps := c.Config.Linear.Steps
	if steps != nil {
		interpolatedCurveValue, err := util.CalculateInterpolatedCurveValue(steps, util.InterpolationTypeLinear, avgTemp/1000)
		if err != nil {
			ui.Error("Error calculating interpolated curve value for sensor '%s': %v", sensor.GetId(), err)
			return 0, err
		}
		value = interpolatedCurveValue
	} else {
		minTemp := float64(c.Config.Linear.Min) * 1000 // degree to milli-degree
		maxTemp := float64(c.Config.Linear.Max) * 1000

		if avgTemp >= maxTemp {
			// full throttle if max temp is reached
			value = 255
		} else if avgTemp <= minTemp {
			// turn fan off if at/below min temp
			value = 0
		} else {
			ratio := (avgTemp - minTemp) / (maxTemp - minTemp)
			value = ratio * 255
		}
	}

	ui.Debug("Evaluating curve '%s'. Sensor '%s' temp '%.0f°'. Desired speed: %.2f", c.Config.ID, sensor.GetId(), avgTemp/1000, value)
	return value, nil
}

// applyHysteresis returns the temperature (in milli-degrees) to evaluate the curve at: the measured one if it is
// above the last one; otherwise, the last one until the measured temperature is more than hysteresis.down below it, and
// then the measured temperature plus hysteresis.down. So a falling temperature lowers the curve value
// hysteresis.down degrees later, and continuously.
func (c *LinearSpeedCurve) applyHysteresis(measured float64) float64 {
	down := c.Config.Linear.GetHysteresisDown() * 1000 // degree to milli-degree
	if down <= 0 {
		return measured
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	temp := measured
	if c.hasHysteresisTemp && measured < c.hysteresisTemp {
		temp = min(c.hysteresisTemp, measured+down)
	}
	c.hysteresisTemp = temp
	c.hasHysteresisTemp = true
	return temp
}

func (c *LinearSpeedCurve) storeResult(value float64, err error, now time.Time) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.Value = value
	}
	c.last = evaluation{err: err, at: now}
	return c.Value, err
}

func (c *LinearSpeedCurve) SetValue(value float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Value = value
}

func (c *LinearSpeedCurve) CurrentValue() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Value
}

func (c *LinearSpeedCurve) CurrentResult() CurveResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.last.result(c.Value)
}
