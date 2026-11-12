package curves

import (
	"fmt"
	"math"
	"sync"

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
	if c.registry == nil {
		return c.CurrentValue(), fmt.Errorf("no registry bound to speed curve '%s'", c.Config.ID)
	}
	sensor, _, err := getSensorReading(c.registry, c.Config.Staircase.Sensor, getClock(c.clock).Now())
	if err != nil {
		return c.CurrentValue(), fmt.Errorf("curve %s: %w", c.Config.ID, err)
	}

	measured := sensor.GetMovingAvg()
	steps := c.Config.Staircase.Steps

	targetTemp := math.MinInt
	for temp := range steps {
		if measured >= float64(temp)*1000 {
			targetTemp = max(targetTemp, temp)
		}
	}
	if targetTemp < c.LastTemp && (c.LastTemp-int(measured/1000)) < c.Config.Staircase.Hysteresis.Down {
		targetTemp = c.LastTemp
	}

	value = steps[targetTemp]

	ui.Debug("Evaluating curve '%s'. Sensor '%s' temp '%.0f°'. Desired speed: %.2f", c.Config.ID, sensor.GetId(), measured/1000, value)
	// LastTemp is written under the lock (together with the value), since the REST API reads it
	c.mu.Lock()
	c.LastTemp = targetTemp
	c.Value = value
	c.mu.Unlock()
	return value, nil
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
