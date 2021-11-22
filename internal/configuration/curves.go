package configuration

type CurveConfig struct {
	Id     string                 `json:"id"`
	Type   string                 `json:"type"`
	Params map[string]interface{} `json:"params"`
}

const (
	LinearCurveType   = "linear"
	FunctionCurveType = "function"
)

type LinearCurveConfig struct {
	Sensor string      `json:"sensor"`
	Min    int         `json:"min"`
	Max    int         `json:"max"`
	Steps  map[int]int `json:"steps"`
}

const (
	FunctionAverage = "average"
	FunctionMinimum = "minimum"
	FunctionMaximum = "maximum"
)

type FunctionCurveConfig struct {
	Type   string   `json:"type"`
	Curves []string `json:"curves"`
}
