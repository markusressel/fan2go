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
	Fans                           []FanConfig
	MaxRpmDiffForSettledFan        float64
}

type SensorConfig struct {
	Id       string    `json:"id"`
	Platform string    `json:"platform"`
	Index    int       `json:"index"`
	Min      float64   `json:"min"`
	Max      float64   `json:"max"`
	Sensors  []*Sensor `json:"sensors"`
}

type FanConfig struct {
	Id        string `json:"id"`
	Platform  string `json:"platform"`
	Fan       int    `json:"fan"`
	NeverStop bool   `json:"neverstop"`
	Sensor    string `json:"sensor"`
	Curve     string `json:"curve"`
}

type CurveConfig struct {
	Id    string      `json:"id"`
	Min   int         `json:"min"`
	Max   int         `json:"max"`
	Steps map[int]int `json:"steps"`
	Type  string      `json:"type"`
}

var CurrentConfig Configuration
