package curves

import (
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
	"math"
)

type linearSpeedCurve struct {
	ID       string
	sensorId string
	min      int
	max      int
	steps    map[int]float64
}

func (c linearSpeedCurve) GetId() string {
	return c.ID
}

func (c linearSpeedCurve) Evaluate() (value int, err error) {
	sensor := sensors.SensorMap[c.sensorId]
	var avgTemp = sensor.GetMovingAvg()

	steps := c.steps
	if steps != nil {
		value = int(math.Round(util.CalculateInterpolatedCurveValue(steps, util.InterpolationTypeLinear, avgTemp/1000)))
	} else {
		minTemp := float64(c.min) * 1000 // degree to milli-degree
		maxTemp := float64(c.max) * 1000

		if avgTemp >= maxTemp {
			// full throttle if max temp is reached
			value = 255
		} else if avgTemp <= minTemp {
			// turn fan off if at/below min temp
			value = 0
		} else {
			ratio := (avgTemp - minTemp) / (maxTemp - minTemp)
			value = int(ratio * 255)
		}
	}

	return value, nil
}
