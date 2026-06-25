package curves

import (
	"fmt"
	"math"
	"sync"

	"github.com/markusressel/fan2go/internal/ui"

	"github.com/markusressel/fan2go/internal/configuration"
)

type StaircaseSpeedCurve struct {
	Config   configuration.CurveConfig `json:"config"`
	Value    float64                   `json:"value"`
	registry RegistryReader

	LastTemp int

	mu sync.RWMutex
}

func (c *StaircaseSpeedCurve) BindRegistry(registry RegistryReader) {
	c.registry = registry
}

func (c *StaircaseSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c *StaircaseSpeedCurve) Evaluate() (value float64, err error) {
	if c.registry == nil {
		return c.Value, fmt.Errorf("no registry bound to speed curve '%s'", c.Config.ID)
	}
	sensor, exists := c.registry.GetSensor(c.Config.Staircase.Sensor)
	if !exists || sensor == nil {
		return c.Value, fmt.Errorf("sensor not found with id '%s'", c.Config.Staircase.Sensor)
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

	c.LastTemp = targetTemp
	value = steps[targetTemp]

	ui.Debug("Evaluating curve '%s'. Sensor '%s' temp '%.0f°'. Desired speed: %.2f", c.Config.ID, sensor.GetId(), measured/1000, value)
	c.SetValue(value)
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
