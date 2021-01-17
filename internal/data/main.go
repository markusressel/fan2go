package data

import (
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/config"
)

type Controller struct {
	Name     string    `json:"name"`
	DType    string    `json:"dtype"`
	Modalias string    `json:"modalias"`
	Platform string    `json:"platform"`
	Path     string    `json:"path"`
	Fans     []*Fan    `json:"fans"`
	Sensors  []*Sensor `json:"sensors"`
}

type Fan struct {
	Name         string                        `json:"name"`
	Index        int                           `json:"index"`
	RpmInput     string                        `json:"rpminput"`
	PwmOutput    string                        `json:"pwmoutput"`
	Config       *config.FanConfig             `json:"config"`
	StartPwm     int                           `json:"startpwm"` // lowest PWM value where the fans are still spinning
	MaxPwm       int                           `json:"maxpwm"`   // highest PWM value that yields an RPM increase
	FanCurveData *map[int]*rolling.PointPolicy `json:"fancurvedata"`
}

type Sensor struct {
	Name   string               `json:"name"`
	Index  int                  `json:"index"`
	Input  string               `json:"string"`
	Config *config.SensorConfig `json:"config"`
	Values *rolling.PointPolicy `json:"values"`
}
