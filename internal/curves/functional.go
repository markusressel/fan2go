package curves

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"math"
)

type FunctionSpeedCurve struct {
	Config configuration.CurveConfig `json:"config"`
	Value  int                       `json:"value"`
}

func (c FunctionSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c FunctionSpeedCurve) Evaluate() (value int, err error) {
	var curves []SpeedCurve
	for _, curveId := range c.Config.Function.Curves {
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

	switch c.Config.Function.Type {
	case configuration.FunctionSum:
		sum := 0
		for _, v := range values {
			sum += v
		}
		value = int(math.Min(255, float64(sum)))
	case configuration.FunctionDifference:
		difference := 0
		for idx, v := range values {
			if idx == 0 {
				difference = v
			} else {
				difference -= v
			}
		}
		value = int(math.Max(0, float64(difference)))
	case configuration.FunctionDelta:
		var dmax = float64(values[0])
		var dmin = float64(values[0])
		for _, v := range values {
			dmin = math.Min(dmin, float64(v))
			dmax = math.Max(dmax, float64(v))
		}
		delta := dmax - dmin
		value = int(delta)
	case configuration.FunctionMinimum:
		var min float64 = 255
		for _, v := range values {
			min = math.Min(min, float64(v))
		}
		value = int(min)
	case configuration.FunctionMaximum:
		var max float64
		for _, v := range values {
			max = math.Max(max, float64(v))
		}
		value = int(max)
	case configuration.FunctionAverage:
		var total = 0
		for _, v := range values {
			total += v
		}
		avg := total / len(curves)
		value = avg
	default:
		ui.Fatal("Unknown curve function: %s", c.Config.Function.Type)
	}

	c.Value = value
	return value, err
}
