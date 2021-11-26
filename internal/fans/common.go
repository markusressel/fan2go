package fans

import (
	"fmt"
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/configuration"
)

const (
	MaxPwmValue       = 255
	MinPwmValue       = 0
	InitialLastSetPwm = -10
)

var (
	FanMap = map[string]Fan{}
)

type Fan interface {
	GetId() string

	GetName() string

	GetConfig() configuration.FanConfig

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
	// IsPwmAuto indicates whether this fan is in "Auto" mode
	IsPwmAuto() (bool, error)

	SetOriginalPwmEnabled(int)
	// GetOriginalPwmEnabled  remembers the "pwm_enabled" state before fan2go took over control
	GetOriginalPwmEnabled() int
	// GetLastSetPwm remembers the last PWM value that has been set for this fan by fan2go
	GetLastSetPwm() int
}

func NewFan(config configuration.FanConfig) (Fan, error) {
	if config.HwMon != nil {
		return &HwMonFan{
			Name:         config.HwMon.Platform,
			Label:        config.ID,
			Index:        config.HwMon.Index,
			PwmOutput:    config.HwMon.PwmOutput,
			RpmInput:     config.HwMon.RpmInput,
			MinPwm:       MinPwmValue,
			MaxPwm:       MaxPwmValue,
			FanCurveData: &map[int]*rolling.PointPolicy{},
			LastSetPwm:   InitialLastSetPwm,
			Config:       config,
		}, nil
	}

	if config.File != nil {
		return &FileFan{}, nil
	}

	return nil, fmt.Errorf("no matching fan type for fan: %s", config.ID)
}
