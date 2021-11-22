package internal

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"math"
	"sort"
)

const (
	InterpolationTypeLinear = "linear"
)

type SpeedCurve interface {
	// Evaluate calculates the current value of the given curve,
	// returns a value in [0..255]
	Evaluate() (value int, err error)
}

type functionSpeedCurve struct {
	function string
	curveIds []string
}

type linearSpeedCurve struct {
	sensorId string
	min      int
	max      int
	steps    map[int]int
}

var (
	SpeedCurveMap = map[string]SpeedCurve{}

	UnknownCurveType = errors.New("unknown curve type")
)

func NewSpeedCurve(curveConfig configuration.CurveConfig) SpeedCurve {
	marshalled, err := json.Marshal(curveConfig.Params)
	if err != nil {
		ui.Error("Couldn't marshal curve configuration: %v", err)
	}

	var speedCurve SpeedCurve
	if curveConfig.Type == configuration.LinearCurveType {
		c := configuration.LinearCurveConfig{}
		if err := json.Unmarshal(marshalled, &c); err != nil {
			ui.Fatal("Couldn't unmarshal curve configuration: %v", err)
		}

		speedCurve = &linearSpeedCurve{
			sensorId: c.Sensor,
			min:      c.Min,
			max:      c.Max,
			steps:    c.Steps,
		}
	} else if curveConfig.Type == configuration.FunctionCurveType {
		c := configuration.FunctionCurveConfig{}
		if err := json.Unmarshal(marshalled, &c); err != nil {
			ui.Error("Couldn't unmarshal curve configuration: %v", err)
		}

		// TODO: what about loops?
		speedCurve = &functionSpeedCurve{
			function: c.Function,
			curveIds: c.Curves,
		}
	} else {
		panic(UnknownCurveType)
	}

	SpeedCurveMap[curveConfig.Id] = speedCurve
	return speedCurve
}

func (c linearSpeedCurve) Evaluate() (value int, err error) {
	sensor := SensorMap[c.sensorId]
	var avgTemp = sensor.GetMovingAvg()

	steps := c.steps
	if steps != nil {
		value = calculateInterpolatedCurveValue(steps, InterpolationTypeLinear, avgTemp/1000)
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

// Creates an interpolated function from the given map of x-values -> y-values
// as specified by the interpolationType and returns the y-value for the given input
func calculateInterpolatedCurveValue(steps map[int]int, interpolationType string, input float64) int {
	xValues := make([]int, 0, len(steps))
	for x := range steps {
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
