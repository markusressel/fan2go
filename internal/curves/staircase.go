package curves

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/markusressel/fan2go/internal/ui"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
)

type StaircaseSpeedCurve struct {
	Config   configuration.CurveConfig `json:"config"`
	Value    float64                   `json:"value"`
	registry RegistryReader

	LastTemp int

	// clock is the time source for the staleness check of the sensor value (nil means util.RealClock)
	clock util.Clock

	// last is the error and time of the last evaluation (guarded by mu, like Value and LastTemp)
	last evaluation

	mu sync.RWMutex
}

// SetClock replaces the time source for the staleness check of the sensor value. Intended for tests.
func (c *StaircaseSpeedCurve) SetClock(clock util.Clock) {
	c.clock = clock
}

func (c *StaircaseSpeedCurve) BindRegistry(registry RegistryReader) {
	c.registry = registry
}

func (c *StaircaseSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c *StaircaseSpeedCurve) Evaluate() (value float64, err error) {
	now := getClock(c.clock).Now()
	value, targetTemp, err := c.evaluate(now)
	if err != nil {
		return c.storeResult(0, err, now)
	}
	// LastTemp is written under the lock (together with the value), since the REST API reads it
	c.mu.Lock()
	defer c.mu.Unlock()
	c.LastTemp = targetTemp
	c.Value = value
	c.last = evaluation{at: now}
	return value, nil
}

// evaluate returns the value of the curve and the temperature of the step it belongs to.
func (c *StaircaseSpeedCurve) evaluate(now time.Time) (value float64, targetTemp int, err error) {
	if c.registry == nil {
		return 0, 0, fmt.Errorf("no registry bound to speed curve '%s'", c.Config.ID)
	}
	sensor, _, err := getSensorReading(c.registry, c.Config.Staircase.Sensor, now)
	if err != nil {
		return 0, 0, fmt.Errorf("curve %s: %w", c.Config.ID, err)
	}

	measured := sensor.GetMovingAvg()
	steps := c.Config.Staircase.Steps

	c.mu.RLock()
	lastTemp := c.LastTemp
	c.mu.RUnlock()

	targetTemp = math.MinInt
	for temp := range steps {
		if measured >= float64(temp)*1000 {
			targetTemp = max(targetTemp, temp)
		}
	}
	if targetTemp < lastTemp && (lastTemp-int(measured/1000)) < c.Config.Staircase.Hysteresis.Down {
		targetTemp = lastTemp
	}

	value = steps[targetTemp]

	ui.Debug("Evaluating curve '%s'. Sensor '%s' temp '%.0f°'. Desired speed: %.2f", c.Config.ID, sensor.GetId(), measured/1000, value)
	return value, targetTemp, nil
}

func (c *StaircaseSpeedCurve) storeResult(value float64, err error, now time.Time) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.Value = value
	}
	c.last = evaluation{err: err, at: now}
	return c.Value, err
}

func (c *StaircaseSpeedCurve) SetValue(value float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Value = value
}

func (c *StaircaseSpeedCurve) CurrentValue() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Value
}

func (c *StaircaseSpeedCurve) CurrentResult() CurveResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.last.result(c.Value)
}
