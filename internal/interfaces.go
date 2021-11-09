package internal

import (
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/configuration"
)

type Sensor interface {
	GetId() string
	GetLabel() string

	GetConfig() *configuration.SensorConfig
	SetConfig(*configuration.SensorConfig)

	// GetValue returns the current value of this sensor
	GetValue() (float64, error)

	// GetMovingAvg returns the moving average of this sensor's value
	GetMovingAvg() float64
	SetMovingAvg(avg float64)
	//Matches(config configuration.SensorConfig) bool
}

type Fan interface {
	GetId() string

	GetName() string

	GetConfig() *configuration.FanConfig
	SetConfig(config *configuration.FanConfig)

	// GetStartPwm returns the min PWM at which the fan starts to rotate from a stand still
	GetStartPwm() int
	SetStartPwm(pwm int)

	// GetMinPwm returns the lowest PWM value where the fans are still spinning, when spinning previously
	GetMinPwm() int
	SetMinPwm(pwm int)

	// GetMaxPwm returns the highest PWM value that yields an RPM increase
	GetMaxPwm() int
	SetMaxPwm(pwm int)

	// GetRpm returns the current RPM value of this fan
	GetRpm() int
	GetRpmAvg() float64
	SetRpmAvg(rpm float64)

	// GetPwm returns the current PWM value of this fan
	GetPwm() int
	SetPwm(pwm int) (err error)

	// GetFanCurveData returns the fan curve data for this fan
	GetFanCurveData() *map[int]*rolling.PointPolicy
	SetFanCurveData(data *map[int]*rolling.PointPolicy)

	// GetPwmEnabled returns the current "pwm_enabled" value of this fan
	GetPwmEnabled() (int, error)
	SetPwmEnabled(value int) (err error)

	// GetOriginalPwmEnabled  remembers the "pwm_enabled" state before fan2go took over control
	GetOriginalPwmEnabled() int
	// GetLastSetPwm remembers the last PWM value that has been set for this fan by fan2go
	GetLastSetPwm() int
}
