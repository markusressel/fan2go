package fans

import (
	"errors"
	"fmt"
	"os"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type HwMonFan struct {
	Label        string                  `json:"label"`
	Index        int                     `json:"index"`
	RpmMovingAvg float64                 `json:"rpmMovingAvg"`
	Config       configuration.FanConfig `json:"config"`
	MinPwm       *int                    `json:"minPwm"`
	StartPwm     *int                    `json:"startPwm"`
	MaxPwm       *int                    `json:"maxPwm"`
	FanCurveData *map[int]float64        `json:"fanCurveData"`
	Rpm          int                     `json:"rpm"`
	Pwm          int                     `json:"pwm"`
}

func (fan *HwMonFan) GetId() string {
	return fan.Config.ID
}

func (fan *HwMonFan) GetMinPwm() int {
	// if the fan is never supposed to stop,
	// use the lowest pwm value where the fan is still spinning
	if fan.ShouldNeverStop() {
		if fan.MinPwm != nil {
			return *fan.MinPwm
		}
	}

	return MinPwmValue
}

func (fan *HwMonFan) SetMinPwm(pwm int, force bool) {
	if fan.Config.MinPwm == nil || force {
		fan.MinPwm = &pwm
	}
}

func (fan *HwMonFan) GetStartPwm() int {
	if fan.StartPwm != nil {
		return *fan.StartPwm
	} else {
		return MaxPwmValue
	}
}

func (fan *HwMonFan) SetStartPwm(pwm int, force bool) {
	if fan.Config.StartPwm == nil || force {
		fan.StartPwm = &pwm
	}
}

func (fan *HwMonFan) GetMaxPwm() int {
	if fan.MaxPwm != nil {
		return *fan.MaxPwm
	} else {
		return MaxPwmValue
	}
}

func (fan *HwMonFan) SetMaxPwm(pwm int, force bool) {
	if fan.Config.MaxPwm == nil || force {
		fan.MaxPwm = &pwm
	}
}

func (fan *HwMonFan) GetRpm() (int, error) {
	if value, err := util.ReadIntFromFile(fan.Config.HwMon.RpmInputPath); err != nil {
		return 0, err
	} else {
		fan.Rpm = value
		return value, nil
	}
}

func (fan *HwMonFan) GetRpmAvg() float64 {
	return fan.RpmMovingAvg
}

func (fan *HwMonFan) SetRpmAvg(rpm float64) {
	fan.RpmMovingAvg = rpm
}

func (fan *HwMonFan) GetPwm() (int, error) {
	value, err := util.ReadIntFromFile(fan.Config.HwMon.PwmPath)
	if err != nil {
		return MinPwmValue, err
	}
	fan.Pwm = value
	return value, nil
}

func (fan *HwMonFan) SetPwm(pwm int) (err error) {
	ui.Debug("Setting Fan PWM of '%s' to %d ...", fan.GetId(), pwm)
	err = util.WriteIntToFile(pwm, fan.Config.HwMon.PwmPath)
	return err
}

func (fan *HwMonFan) GetFanCurveData() *map[int]float64 {
	return fan.FanCurveData
}

// AttachFanCurveData attaches fan curve data from persistence to a fan
// Note: When the given data is incomplete, all values up until the highest
// value in the given dataset will be interpolated linearly
// returns os.ErrInvalid if curveData is void of any data
func (fan *HwMonFan) AttachFanCurveData(curveData *map[int]float64) (err error) {
	if curveData == nil || len(*curveData) <= 0 {
		ui.Error("Cant attach empty fan curve data to fan %s", fan.GetId())
		return os.ErrInvalid
	}

	fan.FanCurveData = curveData

	startPwm, maxPwm := ComputePwmBoundaries(fan)
	fan.SetStartPwm(startPwm, false)
	fan.SetMaxPwm(maxPwm, false)

	// TODO: we don't have a way to determine this yet
	fan.SetMinPwm(startPwm, false)

	return err
}

func (fan *HwMonFan) GetCurveId() string {
	return fan.Config.Curve
}

func (fan *HwMonFan) GetControlAlgorithm() configuration.ControlAlgorithmConfig {
	return fan.Config.ControlAlgorithm
}

func (fan *HwMonFan) ShouldNeverStop() bool {
	return fan.Config.NeverStop
}

func (fan *HwMonFan) GetPwmEnabled() (int, error) {
	return util.ReadIntFromFile(fan.Config.HwMon.PwmEnablePath)
}

func (fan *HwMonFan) IsPwmAuto() (bool, error) {
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
func (fan *HwMonFan) SetPwmEnabled(value ControlMode) (err error) {
	err = util.WriteIntToFile(int(value), fan.Config.HwMon.PwmEnablePath)
	if err == nil {
		currentValue, err := fan.GetPwmEnabled()
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				ui.Warning("Cannot read pwm_enable of fan '%s', pwm_enable state validation cannot work. Continuing assuming it worked.", fan.GetId())
				return nil
			} else if ControlMode(currentValue) != value {
				return fmt.Errorf("PWM mode stuck to %d", currentValue)
			}
		}
	}
	return err
}

func (fan *HwMonFan) Supports(feature FeatureFlag) bool {
	switch feature {
	case FeatureControlMode:
		_, err := os.Stat(fan.Config.HwMon.PwmEnablePath)
		return err == nil
	case FeatureRpmSensor:
		_, err := os.Stat(fan.Config.HwMon.RpmInputPath)
		return err == nil
	}
	return false
}
