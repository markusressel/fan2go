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
	SetPoint float64 `json:"set_point"`
	Kp       float64 `json:"kp"`
	Ki       float64 `json:"ki"`
	Kd       float64 `json:"kd"`
}

const (
	FunctionAverage = "average"
	FunctionDelta   = "delta"
	FunctionMinimum = "minimum"
	FunctionMaximum = "maximum"
)

type FunctionCurveConfig struct {
	Type   string   `json:"type"`
	Curves []string `json:"curves"`
}
