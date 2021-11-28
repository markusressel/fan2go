package fans

import (
	"errors"
	"fmt"
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type HwMonFan struct {
	Name               string                        `json:"name"`
	Label              string                        `json:"label"`
	Index              int                           `json:"index"`
	RpmInput           string                        `json:"rpminput"`
	RpmMovingAvg       float64                       `json:"rpmmovingavg"`
	PwmOutput          string                        `json:"pwmoutput"`
	Config             configuration.FanConfig       `json:"config"`
	StartPwm           int                           `json:"startpwm"` // the min PWM at which the fan starts to rotate from a stand still
	MinPwm             int                           `json:"minpwm"`   // lowest PWM value where the fans are still spinning, when spinning previously
	MaxPwm             int                           `json:"maxpwm"`   // highest PWM value that yields an RPM increase
	FanCurveData       *map[int]*rolling.PointPolicy `json:"fancurvedata"`
	OriginalPwmEnabled int                           `json:"originalpwmenabled"`
	LastSetPwm         int                           `json:"lastsetpwm"`
}

func (fan HwMonFan) GetId() string {
	return fan.Config.ID
}

func (fan HwMonFan) GetName() string {
	return fan.Name
}

func (fan HwMonFan) GetConfig() configuration.FanConfig {
	return fan.Config
}

func (fan HwMonFan) GetStartPwm() int {
	return fan.StartPwm
}

func (fan *HwMonFan) SetStartPwm(pwm int) {
	fan.StartPwm = pwm
}

func (fan HwMonFan) GetMinPwm() int {
	// if the fan is never supposed to stop,
	// use the lowest pwm value where the fan is still spinning
	if fan.GetConfig().NeverStop {
		return fan.MinPwm
	}

	return MinPwmValue
}

func (fan *HwMonFan) SetMinPwm(pwm int) {
	fan.MinPwm = pwm
}

func (fan HwMonFan) GetMaxPwm() int {
	return fan.MaxPwm
}

func (fan *HwMonFan) SetMaxPwm(pwm int) {
	fan.MaxPwm = pwm
}

func (fan HwMonFan) GetRpm() int {
	value, err := util.ReadIntFromFile(fan.RpmInput)
	if err != nil {
		value = -1
	}
	return value
}

func (fan HwMonFan) GetRpmAvg() float64 {
	return fan.RpmMovingAvg
}

func (fan *HwMonFan) SetRpmAvg(rpm float64) {
	fan.RpmMovingAvg = rpm
}

func (fan HwMonFan) GetPwm() int {
	value, err := util.ReadIntFromFile(fan.PwmOutput)
	if err != nil {
		value = MinPwmValue
	}
	return value
}

func (fan *HwMonFan) SetPwm(pwm int) (err error) {
	ui.Debug("Setting %s (%s, %s) to %d ...", fan.Config.ID, fan.Label, fan.Name, pwm)

	err = util.WriteIntToFile(pwm, fan.PwmOutput)
	if err == nil {
		fan.LastSetPwm = pwm
	}
	return err
}

func (fan HwMonFan) GetFanCurveData() *map[int]*rolling.PointPolicy {
	return fan.FanCurveData
}

func (fan *HwMonFan) SetFanCurveData(data *map[int]*rolling.PointPolicy) {
	fan.FanCurveData = data
}

func (fan HwMonFan) GetPwmEnabled() (int, error) {
	pwmEnabledFilePath := fan.PwmOutput + "_enable"
	return util.ReadIntFromFile(pwmEnabledFilePath)
}

func (fan HwMonFan) IsPwmAuto() (bool, error) {
	value, err := fan.GetPwmEnabled()
	if err != nil {
		return false, err
	}
	return value > 1, nil
}

// SetPwmEnabled writes the given value to pwmX_enable
// Possible values (unsure if these are true for all scenarios):
// 0 - no control (results in max speed)
// 1 - manual pwm control
// 2 - motherboard pwm control
func (fan *HwMonFan) SetPwmEnabled(value int) (err error) {
	pwmEnabledFilePath := fan.PwmOutput + "_enable"
	err = util.WriteIntToFile(value, pwmEnabledFilePath)
	if err == nil {
		currentValue, err := util.ReadIntFromFile(pwmEnabledFilePath)
		if err != nil || currentValue != value {
			return errors.New(fmt.Sprintf("PWM mode stuck to %d", currentValue))
		}
	}
	return err
}

func (fan *HwMonFan) SetOriginalPwmEnabled(value int) {
	fan.OriginalPwmEnabled = value
}

func (fan HwMonFan) GetOriginalPwmEnabled() int {
	return fan.OriginalPwmEnabled
}

func (fan HwMonFan) GetLastSetPwm() int {
	return fan.LastSetPwm
}
