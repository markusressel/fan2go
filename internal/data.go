package internal

import (
	"github.com/asecurityteam/rolling"
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
	Name               string                        `json:"name"`
	Index              int                           `json:"index"`
	RpmInput           string                        `json:"rpminput"`
	RpmMovingAvg       float64                       `json:"rpmmovingavg"`
	PwmOutput          string                        `json:"pwmoutput"`
	Config             *FanConfig                    `json:"config"`
	StartPwm           int                           `json:"startpwm"` // lowest PWM value where the fans are still spinning
	MaxPwm             int                           `json:"maxpwm"`   // highest PWM value that yields an RPM increase
	FanCurveData       *map[int]*rolling.PointPolicy `json:"fancurvedata"`
	OriginalPwmEnabled int                           `json:"originalpwmenabled"`
	LastSetPwm         int                           `json:"lastsetpwm"`
}

type Sensor struct {
	Name      string        `json:"name"`
	Index     int           `json:"index"`
	Input     string        `json:"string"`
	Config    *SensorConfig `json:"config"`
	MovingAvg float64       `json:"movingaverage"`
}
