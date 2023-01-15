package configuration

type CurveConfig struct {
	ID       string               `json:"id"`
	Linear   *LinearCurveConfig   `json:"linear,omitempty"`
	PID      *PidCurveConfig      `json:"pid,omitempty"`
	Function *FunctionCurveConfig `json:"function,omitempty"`
}

type LinearCurveConfig struct {
	Sensor string          `json:"sensor"`
	Min    int             `json:"min"`
	Max    int             `json:"max"`
	Steps  map[int]float64 `json:"steps"`
}

type PidCurveConfig struct {
	Sensor   string  `json:"sensor"`
	SetPoint float64 `json:"setPoint"`
	P        float64 `json:"p"`
	I        float64 `json:"i"`
	D        float64 `json:"d"`
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
	Type   string   `json:"type"`
	Curves []string `json:"curves"`
}
