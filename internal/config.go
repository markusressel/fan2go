package internal

import (
	"time"
)

type Configuration struct {
	DbPath                         string
	RunFanInitializationInParallel bool
	TempSensorPollingRate          time.Duration
	RpmPollingRate                 time.Duration
	ControllerAdjustmentTickRate   time.Duration
	TempRollingWindowSize          int
	RpmRollingWindowSize           int
	Sensors                        []SensorConfig
	Curves                         []CurveConfig
	Fans                           []FanConfig
	MaxRpmDiffForSettledFan        float64
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
	Id     string      `json:"id"`
	Type   string      `json:"type"`
	Params interface{} `json:"params"`
}

const LinearCurveType = "linear"

type LinearCurveConfig struct {
	Sensor  string      `json:"sensor"`
	MinTemp int         `json:"min"`
	MaxTemp int         `json:"max"`
	Steps   map[int]int `json:"steps"`
}

var CurrentConfig Configuration
