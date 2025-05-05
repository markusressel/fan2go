package curves

import (
	"github.com/markusressel/fan2go/internal/ui"
	"math"
	"sync"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
)

var (
	valueMu = sync.Mutex{}
)

type LinearSpeedCurve struct {
	Config configuration.CurveConfig `json:"config"`
	Value  int                       `json:"value"`
}

func (c *LinearSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c *LinearSpeedCurve) Evaluate() (value int, err error) {
	sensor, _ := sensors.GetSensor(c.Config.Linear.Sensor)
	var avgTemp = sensor.GetMovingAvg()

	steps := c.Config.Linear.Steps
	if steps != nil {
		value = int(math.Round(util.CalculateInterpolatedCurveValue(steps, util.InterpolationTypeLinear, avgTemp/1000)))
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
			value = int(ratio * 255)
		}
	}

	ui.Debug("Evaluating curve '%s'. Sensor '%s' temp '%.0f°'. Desired PWM: %d", c.Config.ID, sensor.GetId(), avgTemp/1000, value)
	c.SetValue(value)
	return value, nil
}

func (c *LinearSpeedCurve) SetValue(value int) {
	valueMu.Lock()
	defer valueMu.Unlock()
	c.Value = value
}

func (c *LinearSpeedCurve) CurrentValue() int {
	valueMu.Lock()
	defer valueMu.Unlock()
	return c.Value
}
