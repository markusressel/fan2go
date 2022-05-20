package curves

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"math"
)

type functionSpeedCurve struct {
	Config configuration.CurveConfig `json:"config"`
}

func (c functionSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c functionSpeedCurve) Evaluate() (value int, err error) {
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
		var min float64 = 255
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

	ui.Fatal("Unknown curve function: %s", c.Config.Function.Type)
	return value, err
}
