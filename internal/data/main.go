package data

import (
	"fan2go/internal/config"
	"github.com/asecurityteam/rolling"
)

type Controller struct {
	Name     string
	DType    string
	Modalias string
	Platform string
	Path     string
	Fans     []*Fan
	Sensors  []*Sensor
}

type Fan struct {
	Name         string
	Index        int
	RpmInput     string
	PwmOutput    string
	Config       *config.FanConfig
	StartPwm     int // lowest PWM value where the fans are still spinning
	MaxPwm       int // highest PWM value that yields an RPM increase
	FanCurveData *map[int]*rolling.PointPolicy
}

type Sensor struct {
	Name   string
	Index  int
	Input  string
	Config *config.SensorConfig
	Values *rolling.PointPolicy
}
