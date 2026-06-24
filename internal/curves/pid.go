package curves

import (
	"fmt"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type PidSpeedCurve struct {
	Config   configuration.CurveConfig `json:"config"`
	Value    float64                   `json:"value"`
	registry RegistryReader

	pidLoop *util.PidLoop
}

func (c *PidSpeedCurve) BindRegistry(registry RegistryReader) {
	c.registry = registry
}

func (c *PidSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c *PidSpeedCurve) Evaluate() (value float64, err error) {
	if c.registry == nil {
		return c.Value, fmt.Errorf("no registry bound to speed curve '%s'", c.Config.ID)
	}
	sensor, exists := c.registry.GetSensor(c.Config.PID.Sensor)
	if !exists || sensor == nil {
		return c.Value, fmt.Errorf("sensor not found with id '%s'", c.Config.PID.Sensor)
	}
	var measured float64
	measured, err = sensor.GetValue()
	if err != nil {
		ui.Warning("Curve %s: Error getting sensor value: %v", c.Config.ID, err)
		return c.Value, err
	}
	pidTarget := c.Config.PID.SetPoint

	loopValue := c.pidLoop.Loop(pidTarget, measured/1000.0)
	curveValue := loopValue

	ui.Debug("Evaluating curve '%s'. Sensor '%s' temp '%.0f°'. Desired speed: %.2f", c.Config.ID, sensor.GetId(), measured/1000, curveValue)
	c.SetValue(curveValue)
	return curveValue, nil
}

func (c *PidSpeedCurve) SetValue(value float64) {
	valueMu.Lock()
	defer valueMu.Unlock()
	c.Value = value
}

func (c *PidSpeedCurve) CurrentValue() float64 {
	valueMu.Lock()
	defer valueMu.Unlock()
	return c.Value
}
