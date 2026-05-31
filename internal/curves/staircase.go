package curves

import (
	"github.com/markusressel/fan2go/internal/ui"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
)

type StaircaseSpeedCurve struct {
	Config configuration.CurveConfig `json:"config"`
	Value  float64                   `json:"value"`

	LastTemp int
}

func (c *StaircaseSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c *StaircaseSpeedCurve) Evaluate() (value float64, err error) {
	sensor, _ := sensors.GetSensor(c.Config.Staircase.Sensor)
	var measured float64
	measured, err = sensor.GetValue()
	if err != nil {
                ui.Warning("Curve %s: Error getting sensor value: %v", c.Config.ID, err)
                return c.Value, err
        }

	steps := c.Config.Staircase.Steps

	var targetTemp int
	for temp := range steps {
		if measured >= float64(temp)*1000 {
			targetTemp = max(targetTemp, temp)
		}
	}
	if targetTemp < c.LastTemp && (c.LastTemp-int(measured/1000)) < c.Config.Staircase.Threshold {
		targetTemp = c.LastTemp
	}

	c.LastTemp = targetTemp
	value = steps[targetTemp]

	ui.Debug("Evaluating curve '%s'. Sensor '%s' temp '%.0f°'. Desired speed: %.2f", c.Config.ID, sensor.GetId(), measured/1000, value)
	c.SetValue(value)
	return value, nil
}

func (c *StaircaseSpeedCurve) SetValue(value float64) {
	valueMu.Lock()
	defer valueMu.Unlock()
	c.Value = value
}

func (c *StaircaseSpeedCurve) CurrentValue() float64 {
	valueMu.Lock()
	defer valueMu.Unlock()
	return c.Value
}
