package fans

import (
	"errors"
	"fmt"
	"os"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/nvidia_base"
	"github.com/markusressel/fan2go/internal/ui"
)

type NvidiaFan struct {
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

	RunAtMaxSpeed bool // to emulate PWM mode 0
	CanReadRPM    bool
	CanReadPWM    bool
	CanControlFan bool

	device    nvml.Device
	rawDevice nvidia_base.RawNvmlDevice
}

// helper function to turn an nvml error/return code into a go error
// (also handles success by returning nil)
func nvError(errCode nvml.Return) error {
	if errCode == nvml.SUCCESS {
		return nil
	}
	return errors.New(nvml.ErrorString(errCode))
}

func (fan *NvidiaFan) getNvFanIndex() int {
	// fan indices in fan2go are 1-based, here we start at 0.
	return fan.Index - 1
}

func (fan *NvidiaFan) Init() error {
	fan.device, fan.rawDevice = nvidia_base.GetDevice(fan.Config.Nvidia.Device)
	if fan.device == nil {
		return fmt.Errorf("couldn't get handle for nvidia device %s - does it exist?", fan.Config.Nvidia.Device)
	}
	fanIdx := fan.Index - 1 // nvidia uses 0-based index
	numFans, ret := fan.device.GetNumFans()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("couldn't get number of fans from device %s: %s", fan.Config.Nvidia.Device, nvml.ErrorString(ret))
	}
	if fanIdx >= numFans {
		return fmt.Errorf("fan %s has invalid index (%s only has %d fans)", fan.GetId(), fan.Config.Nvidia.Device, numFans)
	}

	// check available features
	_, ret = fan.device.GetFanControlPolicy_v2(fanIdx)
	fan.CanControlFan = (ret == nvml.SUCCESS)

	_, ret = fan.device.GetFanSpeed_v2(fanIdx)
	fan.CanReadPWM = (ret == nvml.SUCCESS)

	fan.CanReadRPM = false
	if fan.rawDevice != nil {
		_, ret = nvidia_base.NvmlGetFanSpeedRPM(fan.rawDevice, fanIdx)
		fan.CanReadRPM = (ret == nvml.SUCCESS)
	}

	return nil
}

func (fan *NvidiaFan) GetId() string {
	return fan.Config.ID
}

func (fan *NvidiaFan) GetMinPwm() int {
	// if the fan is never supposed to stop,
	// use the lowest pwm value where the fan is still spinning
	if fan.ShouldNeverStop() {
		if fan.MinPwm != nil {
			return *fan.MinPwm
		}
	}

	return MinPwmValue
}

func (fan *NvidiaFan) SetMinPwm(pwm int, force bool) {
	if fan.Config.MinPwm == nil || force {
		fan.MinPwm = &pwm
	}
}

func (fan *NvidiaFan) GetStartPwm() int {
	if fan.StartPwm != nil {
		return *fan.StartPwm
	} else {
		// returning MaxPwmValue will make ComputePwmBoundaries()
		// set the StartPwm measured by fan init
		// (otherwise it assumes that the user configured MaxPwm in the config)
		return MaxPwmValue
	}
}

func (fan *NvidiaFan) SetStartPwm(pwm int, force bool) {
	if fan.Config.StartPwm == nil || force {
		fan.StartPwm = &pwm
	}
}

func (fan *NvidiaFan) GetMaxPwm() int {
	if fan.MaxPwm != nil {
		return *fan.MaxPwm
	} else {
		return 100
	}
}

func (fan *NvidiaFan) SetMaxPwm(pwm int, force bool) {
	if fan.Config.MaxPwm == nil || force {
		pwm = min(pwm, 100) // can't be > 100
		fan.MaxPwm = &pwm
	}
}

func (fan *NvidiaFan) GetRpm() (int, error) {
	if !fan.CanReadRPM {
		return -1, fmt.Errorf("fan %d (%s) doesn't support reading RPM", fan.Index, fan.GetId())
	}
	rpm, err := nvidia_base.NvmlGetFanSpeedRPM(fan.rawDevice, fan.getNvFanIndex())
	return rpm, nvError(err)
}

func (fan *NvidiaFan) GetRpmAvg() float64 {
	return fan.RpmMovingAvg
}

func (fan *NvidiaFan) SetRpmAvg(rpm float64) {
	fan.RpmMovingAvg = rpm
}

func (fan *NvidiaFan) GetPwm() (int, error) {
	fanIdx := fan.getNvFanIndex()
	speed, ret := fan.device.GetFanSpeed_v2(fanIdx)
	if ret != nvml.SUCCESS {
		speed = MinPwmValue // this is what HwMonFan does
	}
	// TODO: convert speed from percent (0..100) to PWM (0..255)?
	return int(speed), nvError(ret)
}

func (fan *NvidiaFan) SetPwm(pwm int) (err error) {
	ui.Debug("Setting Fan PWM of '%s' to %d ...", fan.GetId(), pwm)

	fanIdx := fan.getNvFanIndex()
	// TODO: translate pwm (0..255) to percent (0..100)?
	// or just clamp to 100 and let fan2go assume that it doesn't get faster after PWM > 100?
	pwm = min(pwm, 100)
	ret := fan.device.SetFanSpeed_v2(fanIdx, pwm)
	return nvError(ret)
}

func (fan *NvidiaFan) GetFanRpmCurveData() *map[int]float64 {
	return fan.FanCurveData
}

// AttachFanCurveData attaches fan curve data from persistence to a fan
// Note: When the given data is incomplete, all values up until the highest
// value in the given dataset will be interpolated linearly
// returns os.ErrInvalid if curveData is void of any data
func (fan *NvidiaFan) AttachFanRpmCurveData(curveData *map[int]float64) (err error) {
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

func (fan *NvidiaFan) UpdateFanRpmCurveValue(pwm int, rpm float64) {
	if fan.FanCurveData == nil {
		fan.FanCurveData = &map[int]float64{}
	}
	(*fan.FanCurveData)[pwm] = rpm
}

func (fan *NvidiaFan) GetCurveId() string {
	return fan.Config.Curve
}

func (fan *NvidiaFan) ShouldNeverStop() bool {
	return fan.Config.NeverStop
}

func (fan *NvidiaFan) GetPwmEnabled() (int, error) {
	fanIdx := fan.getNvFanIndex()
	policy, err := fan.device.GetFanControlPolicy_v2(fanIdx)
	if err != nvml.SUCCESS {
		return 2, nvError(err)
	}
	pwm := 2 // "motherboard pwm control" as default assumption
	if policy == nvml.FAN_POLICY_MANUAL {
		if fan.RunAtMaxSpeed {
			pwm = 0 // "max speed" - "no control" in hwmon backend
		} else {
			pwm = 1 // manual PWM control
		}
	}
	return pwm, nil
}

func (fan *NvidiaFan) IsPwmAuto() (bool, error) {
	value, err := fan.GetPwmEnabled()
	if err != nil {
		return true, err // assume auto control by default
	}
	return value == 2, nil
}

// SetPwmEnabled writes the given value to pwmX_enable
// Possible values (unsure if these are true for all scenarios):
// 0 - no control (results in max speed)
// 1 - manual pwm control
// 2 - motherboard pwm control
func (fan *NvidiaFan) SetPwmEnabled(value ControlMode) (err error) {
	device := fan.device
	fanIdx := fan.getNvFanIndex()
	var ret nvml.Return
	if value == 2 {
		ret = nvml.DeviceSetDefaultFanSpeed_v2(device, fanIdx)
	} else {
		// TODO: really support mode 0? here mode 2 is default and mode 0 must be emulated
		if value == 0 {
			fan.RunAtMaxSpeed = true
			ret = nvml.DeviceSetFanSpeed_v2(device, fanIdx, 100)
		} else {
			ret = device.SetFanControlPolicy(fanIdx, nvml.FAN_POLICY_MANUAL)
			// TODO: set speed? just setting a speed implicitly sets manual policy - if so, what speed?
		}
	}
	return nvError(ret)
}

func (fan *NvidiaFan) GetConfig() configuration.FanConfig {
	return fan.Config
}

func (fan *NvidiaFan) Supports(feature FeatureFlag) bool {
	switch feature {
	case FeatureControlMode:
		return fan.CanControlFan
	case FeaturePwmSensor:
		// FIXME: ugly workaround. Not allowing to read the PWM sensor works around an issue in
		// `fan init`, in the first step where it rapidly sets and gets all PWM values, which doesn't
		// work well for NvidiaFan, because device.GetFanSpeed_v2(), used by GetPwm(), returns the
		// *current* fan speed (in percent), so it only returns the correct value after the fan had
		// time to spin up/down (and even then it might be off by 1 or so due to speed fluctuations)
		return false
		//return fan.CanReadPWM
	case FeatureRpmSensor:
		return fan.CanReadRPM
	}
	return false
}
