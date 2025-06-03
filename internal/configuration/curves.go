package configuration

type CurveConfig struct {
	// ID is the id of the curve
	ID string `json:"id"`

	// can be any of the following:
	Linear   *LinearCurveConfig   `json:"linear,omitempty"`
	PID      *PidCurveConfig      `json:"pid,omitempty"`
	Function *FunctionCurveConfig `json:"function,omitempty"`
}

type LinearCurveConfig struct {
	// Sensor is the id of the sensor to use for this curve
	Sensor string `json:"sensor"`
	// Min is the minimum temperature in degrees
	Min int `json:"min"`
	// Max is the maximum temperature in degrees
	Max int `json:"max"`
	// Steps is a map of temperature to relative speed value (in range of 0..255)
	Steps      map[int]string
	FloatSteps map[int]float64 `json:"steps"` // FIXME: what is `json:"steps"` used for? should it be here or on Steps?
}

type PidCurveConfig struct {
	// Sensor is the id of the sensor to use for this curve
	Sensor string `json:"sensor"`
	// SetPoint is the target temperature in degrees
	SetPoint float64 `json:"setPoint"`
	// P is the proportional gain
	P float64 `json:"p"`
	// I is the integral gain
	I float64 `json:"i"`
	// D is the derivative gain
	D float64 `json:"d"`
}

const (
	// FunctionSum computes the sum of all referenced curves
	FunctionSum = "sum"
	// FunctionDifference computes the difference of all referenced curves
	FunctionDifference = "difference"
	// FunctionAverage computes the average value of all referenced
	// curves using the arithmetic mean
	FunctionAverage = "average"
	// FunctionDelta computes the difference between the biggest and the smallest
	// value of all referenced curves
	FunctionDelta = "delta"
	// FunctionMinimum computes the smallest value of all referenced curves
	FunctionMinimum = "minimum"
	// FunctionMaximum computes the biggest value of all referenced curves
	FunctionMaximum = "maximum"
)

type FunctionCurveConfig struct {
	// Type is the type of function to use, can be one of the following:
	// sum, difference, average, delta, minimum, maximum
	Type string `json:"type"`
	// Curves is a list of other curve ids to use as input for the defined function type
	Curves []string `json:"curves"`
}
