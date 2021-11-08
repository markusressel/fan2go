package internal

import (
	"time"
)

type Configuration struct {
	DbPath                         string         `json:"dbPath"`
	RunFanInitializationInParallel bool           `json:"runFanInitializationInParallel"`
	TempSensorPollingRate          time.Duration  `json:"tempSensorPollingRate"`
	RpmPollingRate                 time.Duration  `json:"rpmPollingRate"`
	ControllerAdjustmentTickRate   time.Duration  `json:"controllerAdjustmentTickRate"`
	TempRollingWindowSize          int            `json:"tempRollingWindowSize"`
	RpmRollingWindowSize           int            `json:"rpmRollingWindowSize"`
	Sensors                        []SensorConfig `json:"sensors"`
	Curves                         []CurveConfig  `json:"curves"`
	Fans                           []FanConfig    `json:"fans"`
	MaxRpmDiffForSettledFan        float64        `json:"maxRpmDiffForSettledFan"`
}

type SensorConfig struct {
	Id       string `json:"id"`
	Platform string `json:"platform"`
	Index    int    `json:"index"`
}

type FanConfig struct {
	Id        string `json:"id"`
	Platform  string `json:"platform"`
	Fan       int    `json:"fan"`
	NeverStop bool   `json:"neverstop"`
	Curve     string `json:"curve"`
}

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
	Sensor  string      `json:"sensor"`
	MinTemp int         `json:"minTemp"`
	MaxTemp int         `json:"maxTemp"`
	Steps   map[int]int `json:"steps"`
}

const (
	FunctionAverage = "average"
	FunctionMinimum = "minimum"
	FunctionMaximum = "maximum"
)

type FunctionCurveConfig struct {
	Function string   `json:"function"`
	Curves   []string `json:"curves"`
}

var CurrentConfig Configuration
