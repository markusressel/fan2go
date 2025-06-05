package curves

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type PidSpeedCurve struct {
	Config configuration.CurveConfig `json:"config"`
	Value  float64                   `json:"value"`

	pidLoop *util.PidLoop
}

func (c *PidSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c *PidSpeedCurve) Evaluate() (value float64, err error) {
	sensor, _ := sensors.GetSensor(c.Config.PID.Sensor)
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
