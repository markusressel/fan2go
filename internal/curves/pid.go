package curves

import (
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
)

type pidSpeedCurve struct {
	ID       string
	sensorId string
	setPoint float64

	pidLoop *util.PidLoop
}

func (c pidSpeedCurve) GetId() string {
	return c.ID
}

func (c pidSpeedCurve) Evaluate() (value int, err error) {
	sensor := sensors.SensorMap[c.sensorId]
	measured, _ := sensor.GetValue()
	pidTarget := c.setPoint

	loopValue := c.pidLoop.Loop(pidTarget, measured/1000.0)

	// clamp to (0..1)
	if loopValue > 1 {
		loopValue = 1
	} else if loopValue < 0 {
		loopValue = 0
	}

	// map to expected output range
	curveValue := int(loopValue * 255)

	return curveValue, nil
}
