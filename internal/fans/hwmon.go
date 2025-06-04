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

	// lastKnownAutomaticControlMode stores the last known automatic control mode value (pwm_enable) for this fan.
	// This value is used when fan2go requests to set the control mode to automatic.
	lastKnownAutomaticControlMode *int
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

func (fan *HwMonFan) GetFanRpmCurveData() *map[int]float64 {
	return fan.FanCurveData
}

// AttachFanCurveData attaches fan curve data from persistence to a fan
// Note: When the given data is incomplete, all values up until the highest
// value in the given dataset will be interpolated linearly
// returns os.ErrInvalid if curveData is void of any data
func (fan *HwMonFan) AttachFanRpmCurveData(curveData *map[int]float64) (err error) {
	if curveData == nil || len(*curveData) <= 0 {
		ui.Error("Can't attach empty fan curve data to fan %s", fan.GetId())
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

func (fan *HwMonFan) UpdateFanRpmCurveValue(pwm int, rpm float64) {
	if fan.FanCurveData == nil {
		fan.FanCurveData = &map[int]float64{}
	}
	(*fan.FanCurveData)[pwm] = rpm
}

func (fan *HwMonFan) GetCurveId() string {
	return fan.Config.Curve
}

func (fan *HwMonFan) ShouldNeverStop() bool {
	return fan.Config.NeverStop
}

func (fan *HwMonFan) GetControlMode() (ControlMode, error) {
	pwmEnabledValue, err := util.ReadIntFromFile(fan.Config.HwMon.PwmEnablePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ui.Warning("pwm_enable file for fan '%s' does not exist, assuming no control mode support", fan.GetId())
			return ControlModeDisabled, nil
		}
		return ControlModeUnknown, fmt.Errorf("error reading pwm_enable for fan %s: %w", fan.GetId(), err)
	}

	if pwmEnabledValue >= 2 {
		// if we have a pwm_enable value >= 2, store it as the last known automatic control mode
		// so that we can use it when setting the control mode to automatic later
		fan.lastKnownAutomaticControlMode = &pwmEnabledValue
		return ControlModeAutomatic, nil
	}

	switch pwmEnabledValue {
	case 0:
		return ControlModeDisabled, nil
	case 1:
		return ControlModePWM, nil
	default:
		return ControlModeUnknown, fmt.Errorf("cannot map pwm_enable value %d to ControlMode for fan %s", pwmEnabledValue, fan.GetId())
	}
}

// SetControlMode writes the given value to pwmX_enable
//
// The values that are supported for pwmX_enable depend on the
// specific hardware and driver implementation.
//
// The most common arrangement is:
// 0 - no control (results in max speed)
// 1 - manual pwm control
// 2 - motherboard pwm control
//
// Note that not all drivers only use values in [0, 1, 2].
// F.ex. the "nct6775" driver, which is used for the "nct6798" chip uses:
// 0 - Fan control disabled (fans set to maximum speed)
// 1 - Manual mode, write to pwm[0-5] any value 0-255
// 2 - "Thermal Cruise" mode (set target temperature in pwm[1-7]_target_temp and pwm[1-7]_target_temp_tolerance)
// 3 - "Fan Speed Cruise" mode (set target fan speed with fan[1-7]_target and fan[1-7]_tolerance)
// 4 - "Smart Fan III" mode (NCT6775F only) (presumably similar to 5)
// 5 - "Smart Fan IV" mode (uses a configurable curve)
//
// Any value >= 2 will be considered as "automatic control mode" by fan2go,
// and will be stored as the last known automatic control mode for this fan.
func (fan *HwMonFan) SetControlMode(value ControlMode) (err error) {
	var pwmEnabledValue int
	switch value {
	case ControlModeDisabled:
		pwmEnabledValue = 0
	case ControlModePWM:
		pwmEnabledValue = 1
	case ControlModeAutomatic:
		if fan.lastKnownAutomaticControlMode != nil {
			// if we have a last known automatic control mode, use that
			pwmEnabledValue = *fan.lastKnownAutomaticControlMode
		} else {
			// otherwise assume 2 as default for automatic control
			ui.Warning("No last known automatic control mode for fan '%s', assuming 2 (automatic control)", fan.GetId())
			pwmEnabledValue = 2
		}
	}

	err = util.WriteIntToFile(pwmEnabledValue, fan.Config.HwMon.PwmEnablePath)
	if err == nil {
		currentValue, err := fan.GetControlMode()
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				ui.Warning("Cannot read pwm_enable of fan '%s', pwm_enable state validation cannot work. Continuing assuming it worked.", fan.GetId())
				return nil
			} else if currentValue != value {
				return fmt.Errorf("PWM mode stuck to %d", currentValue)
			}
		}
	}
	return err
}

func (fan *HwMonFan) GetConfig() configuration.FanConfig {
	return fan.Config
}

func (fan *HwMonFan) Supports(feature FeatureFlag) bool {
	switch feature {
	case FeatureControlMode:
		_, err := os.Stat(fan.Config.HwMon.PwmEnablePath)
		return err == nil
	case FeaturePwmSensor:
		_, err := util.ReadIntFromFile(fan.Config.HwMon.PwmPath)
		return err == nil
	case FeatureRpmSensor:
		_, err := os.Stat(fan.Config.HwMon.RpmInputPath)
		return err == nil
	}
	return false
}
