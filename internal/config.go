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
}

type SensorConfig struct {
	Id       string
	Platform string
	Index    int
	Min      float64
	Max      float64
}

type FanConfig struct {
	Id        string `json:"id"`
	Platform  string `json:"platform"`
	Fan       int    `json:"fan"`
	NeverStop bool   `json:"neverstop"`
	Sensor    string `json:"sensor"`
}

var CurrentConfig Configuration
