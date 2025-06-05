package curves

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"math"
)

type FunctionSpeedCurve struct {
	Config configuration.CurveConfig `json:"config"`
	Value  float64                   `json:"value"`
}

func (c *FunctionSpeedCurve) GetId() string {
	return c.Config.ID
}

func (c *FunctionSpeedCurve) Evaluate() (value float64, err error) {
	var curves []SpeedCurve
	for _, curveId := range c.Config.Function.Curves {
		curve, _ := GetSpeedCurve(curveId)
		curves = append(curves, curve)
	}

	var values []float64
	for _, curve := range curves {
		// TODO: if the curve is also used in another fan controller, this will cause multiple calls to Evaluate
		//  which messes up the algorithm since the controller expects that the curve is evaluated only
		//  once per cycle,
		//  The only way to fix this that comes to mind is to update the value of each curve in a separate
		//  goroutine that runs independently and only retrieve its current value in the fan controller.
		//  This might cause additional race-conditions though.
		//  This would also allow external tools like fan2go-tui to show the current value of the curve
		//  even if it is currently not used by any fan controller.
		v, err := curve.Evaluate()
		if err != nil {
			return 0, err
		}
		values = append(values, v)
	}

	switch c.Config.Function.Type {
	case configuration.FunctionSum:
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		value = math.Min(255, sum)
	case configuration.FunctionDifference:
		difference := 0.0
		for idx, v := range values {
			if idx == 0 {
				difference = v
			} else {
				difference -= v
			}
		}
		value = math.Max(0, difference)
	case configuration.FunctionDelta:
		var dmax = values[0]
		var dmin = values[0]
		for _, v := range values {
			dmin = math.Min(dmin, v)
			dmax = math.Max(dmax, v)
		}
		delta := dmax - dmin
		value = delta
	case configuration.FunctionMinimum:
		var min float64 = 255
		for _, v := range values {
			min = math.Min(min, v)
		}
		value = min
	case configuration.FunctionMaximum:
		var max float64
		for _, v := range values {
			max = math.Max(max, float64(v))
		}
		value = max
	case configuration.FunctionAverage:
		var total = 0.0
		for _, v := range values {
			total += v
		}
		avg := total / float64(len(curves))
		value = avg
	default:
		ui.Fatal("Unknown curve function: %s", c.Config.Function.Type)
	}

	ui.Debug("Evaluating curve '%s'. Curve values: '%v' Desired speed: %.2f", c.Config.ID, values, value)
	c.SetValue(value)
	return value, err
}

func (c *FunctionSpeedCurve) SetValue(value float64) {
	valueMu.Lock()
	defer valueMu.Unlock()
	c.Value = value
}

func (c *FunctionSpeedCurve) CurrentValue() float64 {
	valueMu.Lock()
	defer valueMu.Unlock()
	return c.Value
}
