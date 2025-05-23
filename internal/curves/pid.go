package curves

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"math"
)

type PidSpeedCurve struct {
	Config configuration.CurveConfig `json:"config"`
	Value  int                       `json:"value"`

	pidLoop *util.PidLoop
}

func (c *PidSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c *PidSpeedCurve) Evaluate() (value int, err error) {
	sensor, _ := sensors.GetSensor(c.Config.PID.Sensor)
	var measured float64
	measured, err = sensor.GetValue()
	if err != nil {
		ui.Warning("Curve %s: Error getting sensor value: %v", c.Config.ID, err)
		return c.Value, err
	}
	pidTarget := c.Config.PID.SetPoint

	loopValue := c.pidLoop.Loop(pidTarget, measured/1000.0)
	curveValue := int(math.Round(loopValue))

	ui.Debug("Evaluating curve '%s'. Sensor '%s' temp '%.0f°'. Desired PWM: %d", c.Config.ID, sensor.GetId(), measured/1000, curveValue)
	c.SetValue(curveValue)
	return curveValue, nil
}

func (c *PidSpeedCurve) SetValue(value int) {
	valueMu.Lock()
	defer valueMu.Unlock()
	c.Value = value
}

func (c *PidSpeedCurve) CurrentValue() int {
	valueMu.Lock()
	defer valueMu.Unlock()
	return c.Value
}
