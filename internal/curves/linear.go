package curves

import (
	"github.com/markusressel/fan2go/internal/ui"
	"math"
	"strconv"
	"strings"
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

func (c *LinearSpeedCurve) Init() {
	cfg := c.Config.Linear
	cfg.FloatSteps = make(map[int]float64)

	for temp, origstr := range cfg.Steps {
		str := strings.TrimSpace(origstr)
		l := len(str)
		isPercent := false
		if l > 1 && str[l-1] == '%' {
			isPercent = true
			str = str[:l-1] // cut off '%' because ParseFloat() wouldn't like it
		}
		speed, err := strconv.ParseFloat(str, 64)
		if err != nil {
			ui.Warning("Invalid curve step value '%s' in %s", origstr, c.Config.ID)
		} else {
			if isPercent {
				// convert from 1..100 percent to 0..255
				speed = speed * 2.55
			}
			cfg.FloatSteps[temp] = speed
		}
	}
}

func (c *LinearSpeedCurve) GetId() string {
	return c.Config.ID
}

// FIXME: value should be a float - it gets interpolated anyway!
func (c *LinearSpeedCurve) Evaluate() (value int, err error) {
	sensor, _ := sensors.GetSensor(c.Config.Linear.Sensor)
	var avgTemp = sensor.GetMovingAvg()

	steps := c.Config.Linear.FloatSteps
	if steps != nil {
		interpolatedCurveValue, err := util.CalculateInterpolatedCurveValue(steps, util.InterpolationTypeLinear, avgTemp/1000)
		if err != nil {
			ui.Error("Error calculating interpolated curve value for sensor '%s': %v", sensor.GetId(), err)
			return 0, err
		}
		value = int(math.Round(interpolatedCurveValue))
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
