package internal

import (
	"encoding/json"
	"errors"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"math"
	"sort"
)

const (
	InterpolationTypeLinear = "linear"
)

var UnknownCurveType = errors.New("unknown curve type")

// Calculates the current value of the given curve
// returns a value in [0..255]
func evaluateCurve(curve configuration.CurveConfig) (value int, err error) {
	// TODO: implement response delay
	// TODO: implement some kind of "rapid increase" when the upper
	//  limit temperature limit is reached

	// this manual marshalling isn't pretty, but afaik viper
	// doesn't have a built-in mechanism to parse configuration subtrees based on application logic
	marshalled, err := json.Marshal(curve.Params)
	if err != nil {
		ui.Error("Couldn't marshal curve configuration: %v", err)
	}

	if curve.Type == configuration.LinearCurveType {
		c := configuration.LinearCurveConfig{}
		if err := json.Unmarshal(marshalled, &c); err != nil {
			ui.Fatal("Couldn't unmarshal curve configuration: %v", err)
		}

		return evaluateLinearCurve(c)
	} else if curve.Type == configuration.FunctionCurveType {
		c := configuration.FunctionCurveConfig{}
		if err := json.Unmarshal(marshalled, &c); err != nil {
			ui.Error("Couldn't unmarshal curve configuration: %v", err)
		}

		return evaluateFunctionCurve(c)
	}

	return 0, UnknownCurveType
}

func evaluateLinearCurve(c configuration.LinearCurveConfig) (value int, err error) {
	sensor := SensorMap[c.Sensor]
	var avgTemp = sensor.GetMovingAvg()

	steps := c.Steps
	if steps != nil {
		value = calculateInterpolatedCurveValue(steps, InterpolationTypeLinear, avgTemp/1000)
	} else {
		minTemp := float64(c.Min) * 1000 // degree to milli-degree
		maxTemp := float64(c.Max) * 1000

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

// Creates an interpolated function from the given map of x-values -> y-values
// as specified by the interpolationType and returns the y-value for the given input
func calculateInterpolatedCurveValue(steps map[int]int, interpolationType string, input float64) int {
	xValues := make([]int, 0, len(steps))
	for x, _ := range steps {
		xValues = append(xValues, int(x))
	}
	// sort them increasing
	sort.Ints(xValues)

	// find value closest to input
	for i := 0; i < len(xValues)-1; i++ {
		currentX := xValues[i]
		nextX := xValues[i+1]

		if input <= float64(currentX) && i == 0 {
			// input is below the smallest given step, so
			// we fall back to the value of the smallest step
			return steps[currentX]
		}

		if input >= float64(nextX) {
			continue
		}

		if input == float64(currentX) {
			return steps[currentX]
		} else {
			// input is somewhere in between currentX and nextX
			currentY := float64(steps[currentX])
			nextY := float64(steps[nextX])

			ratio := util.Ratio(input, float64(currentX), float64(nextX))
			interpolation := currentY + ratio*(nextY-currentY)
			return int(math.Round(interpolation))
		}
	}

	// input is above (or equal to) the largest given
	// step, so we fall back to the value of the largest step
	return steps[xValues[len(xValues)-1]]
}

func evaluateFunctionCurve(c configuration.FunctionCurveConfig) (value int, err error) {
	var curves []configuration.CurveConfig
	for _, curveId := range c.Curves {
		curves = append(curves, *CurveMap[curveId])
	}

	if c.Function == configuration.FunctionMinimum {
		var min int
		for _, curve := range curves {
			v, err := evaluateCurve(curve)
			if err != nil {
				return 0, err
			}

			min = int(math.Min(float64(min), float64(v)))
		}
		value = min
	} else if c.Function == configuration.FunctionMaximum {
		var max int
		for _, curve := range curves {
			v, err := evaluateCurve(curve)
			if err != nil {
				return 0, err
			}

			max = int(math.Max(float64(max), float64(v)))
		}
		value = max
	} else if c.Function == configuration.FunctionAverage {
		var total = 0
		for _, curve := range curves {
			v, err := evaluateCurve(curve)
			if err != nil {
				return 0, err
			}

			total += v
		}
		value = total / len(curves)
	} else {
		ui.Fatal("Unknown curve function: %s", c.Function)
	}

	return value, err
}
