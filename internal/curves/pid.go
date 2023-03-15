package curves

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
)

type PidSpeedCurve struct {
	Config configuration.CurveConfig `json:"config"`
	Value  int                       `json:"value"`

	pidLoop *util.PidLoop
}

func (c PidSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c PidSpeedCurve) Evaluate() (value int, err error) {
	sensor := sensors.SensorMap[c.Config.PID.Sensor]
	var measured float64
	measured, err = sensor.GetValue()
	pidTarget := c.Config.PID.SetPoint

	loopValue := c.pidLoop.Loop(pidTarget, measured/1000.0)

	// clamp to (0..1)
	if loopValue > 1 {
		loopValue = 1
	} else if loopValue < 0 {
		loopValue = 0
	}

	// map to expected output range
	curveValue := int(loopValue * 255)

	c.Value = curveValue
	return curveValue, nil
}
