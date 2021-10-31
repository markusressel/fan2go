package internal

import (
	"errors"
)

var UnknownCurveType = errors.New("unknown curve type")

// Calculates the current value of the given curve
// returns a value in [0..255]
func evaluateCurve(curve CurveConfig) (value int, err error) {
	// TODO: implement response delay
	// TODO: implement some kind of "rapid increase" when the upper
	//  limit temperature limit is reached

	if curve.Type == LinearCurveType {
		config := curve.Params.(LinearCurveConfig)
		return evaluateLinearCurve(config)
	} else if curve.Type == FunctionCurveType {
		config := curve.Params.(FunctionCurveConfig)
		return evaluateFunctionCurve(config)
	}

	return 0, UnknownCurveType
}

func evaluateLinearCurve(config LinearCurveConfig) (value int, err error) {
	sensor := SensorMap[config.Sensor]

	minTemp := float64(config.MinTemp) * 1000 // degree to milli-degree
	maxTemp := float64(config.MaxTemp) * 1000

	var avgTemp = sensor.MovingAvg

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

	return value, nil
}

func evaluateFunctionCurve(config FunctionCurveConfig) (value int, err error) {
	var curves []CurveConfig
	for _, curveId := range config.Curves {
		curves = append(curves, *CurveMap[curveId])
	}

	if config.Function == FunctionAverage {
		var total = 0
		for _, curve := range curves {
			v, err := evaluateCurve(curve)
			if err != nil {
				return 0, err
			}

			total += v
		}
		value = total / len(curves)
	}

	return value, err
}

func FunctionCurve(
	function string,
	values []int,
) (result int) {
	result = 0
	if function == "average" {
		result = 0
		var total = 0
		for _, value := range values {
			total += value
		}
		result = total / len(values)
	}
	return result
}

func LinearCurve(
	config CurveConfig,
	sensorValues []int,
) (target int) {
	return 0
}
