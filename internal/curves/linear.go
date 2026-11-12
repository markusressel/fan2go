package curves

import (
	"fmt"
	"sync"

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
	if c.registry == nil {
		return c.CurrentValue(), fmt.Errorf("no registry bound to speed curve '%s'", c.Config.ID)
	}
	sensor, _, err := getSensorReading(c.registry, c.Config.Linear.Sensor, getClock(c.clock).Now())
	if err != nil {
		return c.CurrentValue(), fmt.Errorf("curve %s: %w", c.Config.ID, err)
	}
	var avgTemp = sensor.GetMovingAvg()

	steps := c.Config.Linear.Steps
	if steps != nil {
		interpolatedCurveValue, err := util.CalculateInterpolatedCurveValue(steps, util.InterpolationTypeLinear, avgTemp/1000)
		if err != nil {
			ui.Error("Error calculating interpolated curve value for sensor '%s': %v", sensor.GetId(), err)
			return c.CurrentValue(), err
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
	c.SetValue(value)
	return value, nil
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
