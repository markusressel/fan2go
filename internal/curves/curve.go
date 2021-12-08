package curves

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"math"
)

type SpeedCurve interface {
	GetId() string
	// Evaluate calculates the current value of the given curve,
	// returns a value in [0..255]
	Evaluate() (value int, err error)
}

type functionSpeedCurve struct {
	ID       string
	function string
	curveIds []string
}

type linearSpeedCurve struct {
	ID       string
	sensorId string
	min      int
	max      int
	steps    map[int]float64
}

var (
	SpeedCurveMap = map[string]SpeedCurve{}
)

func NewSpeedCurve(config configuration.CurveConfig) (SpeedCurve, error) {
	if config.Linear != nil {
		return &linearSpeedCurve{
			ID:       config.ID,
			sensorId: config.Linear.Sensor,
			min:      config.Linear.Min,
			max:      config.Linear.Max,
			steps:    config.Linear.Steps,
		}, nil
	}

	if config.Function != nil {
		return &functionSpeedCurve{
			ID:       config.ID,
			function: config.Function.Type,
			curveIds: config.Function.Curves,
		}, nil
	}

	return nil, fmt.Errorf("no matching curve type for curve: %s", config.ID)
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

func (c functionSpeedCurve) GetId() string {
	return c.ID
}

func (c functionSpeedCurve) Evaluate() (value int, err error) {
	var curves []SpeedCurve
	for _, curveId := range c.curveIds {
		curves = append(curves, SpeedCurveMap[curveId])
	}

	var values []int
	for _, curve := range curves {
		v, err := curve.Evaluate()
		if err != nil {
			return 0, err
		}
		values = append(values, v)
	}

	switch c.function {
    case configuration.FunctionDelta:
		var dmax = float64(values[0])
		var dmin = float64(values[0])
		for _, v := range values {
			dmin = math.Min(dmin, float64(v))
			dmax = math.Max(dmax, float64(v))
		}
		delta := dmax - dmin
		return int(delta), nil
	case configuration.FunctionMinimum:
		var min float64
		for _, v := range values {
			min = math.Min(min, float64(v))
		}
		return int(min), nil
	case configuration.FunctionMaximum:
		var max float64
		for _, v := range values {
			max = math.Max(max, float64(v))
		}
		return int(max), nil
	case configuration.FunctionAverage:
		var total = 0
		for _, v := range values {
			total += v
		}
		avg := total / len(curves)
		return avg, nil
	}

	ui.Fatal("Unknown curve function: %s", c.function)
	return value, err
}
