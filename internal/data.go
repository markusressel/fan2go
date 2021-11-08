package internal

import (
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
)

type Controller struct {
	Name     string                `json:"name"`
	DType    string                `json:"dtype"`
	Modalias string                `json:"modalias"`
	Platform string                `json:"platform"`
	Path     string                `json:"path"`
	Fans     []*Fan                `json:"fans"`
	Sensors  []sensors.HwmonSensor `json:"sensors"`
}

type Fan struct {
	Name               string                        `json:"name"`
	Label              string                        `json:"label"`
	Index              int                           `json:"index"`
	RpmInput           string                        `json:"rpminput"`
	RpmMovingAvg       float64                       `json:"rpmmovingavg"`
	PwmOutput          string                        `json:"pwmoutput"`
	Config             *configuration.FanConfig      `json:"config"`
	StartPwm           int                           `json:"startpwm"` // the min PWM at which the fan starts to rotate from a stand still
	MinPwm             int                           `json:"minpwm"`   // lowest PWM value where the fans are still spinning, when spinning previously
	MaxPwm             int                           `json:"maxpwm"`   // highest PWM value that yields an RPM increase
	FanCurveData       *map[int]*rolling.PointPolicy `json:"fancurvedata"`
	OriginalPwmEnabled int                           `json:"originalpwmenabled"`
	LastSetPwm         int                           `json:"lastsetpwm"`
}

type Sensor interface {
	GetId() string
	GetLabel() string
	GetConfig() *configuration.SensorConfig
	SetConfig(*configuration.SensorConfig)
	GetValue() (float64, error)

	GetMovingAvg() float64
	SetMovingAvg(avg float64)
}
