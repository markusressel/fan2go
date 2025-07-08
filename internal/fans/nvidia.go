//go:build !disable_nvml

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

	RunAtMaxSpeed     bool // to emulate PWM mode 0
	CanReadRPM        bool
	CanReadPWM        bool
	CanSetControlMode bool

	device    nvml.Device
	rawDevice nvidia_base.RawNvmlDevice
}

func CreateNvidiaFan(config configuration.FanConfig) (Fan, error) {
	ret := &NvidiaFan{
		Label:    config.ID,
		Index:    config.Nvidia.Index,
		MinPwm:   config.MinPwm,
		StartPwm: config.StartPwm,
		MaxPwm:   config.MaxPwm,
		Config:   config,
	}
	err := ret.Init()
	if err != nil {
		return nil, err
	}
	return ret, nil
}

// helper function to turn a nvml error/return code into a go error
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
	fanIdx := fan.getNvFanIndex()
	numFans, ret := fan.device.GetNumFans()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("couldn't get number of fans from device %s: %s", fan.Config.Nvidia.Device, nvml.ErrorString(ret))
	}
	if fanIdx >= numFans {
		return fmt.Errorf("fan %s has invalid index (%s only has %d fans)", fan.GetId(), fan.Config.Nvidia.Device, numFans)
	}

	// check available features
	_, ret = fan.device.GetFanControlPolicy_v2(fanIdx)
	fan.CanSetControlMode = (ret == nvml.SUCCESS)

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

func (fan *NvidiaFan) GetLabel() string {
	return fan.Label
}

func (fan *NvidiaFan) GetIndex() int {
	return fan.Index
}

func (fan *NvidiaFan) GetMinPwm() int {
	if fan.MinPwm != nil {
		return *fan.MinPwm
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
	return int(speed), nvError(ret)
}

func (fan *NvidiaFan) SetPwm(pwm int) (err error) {
	ui.Debug("Setting Fan PWM of '%s' to %d ...", fan.GetId(), pwm)

	fanIdx := fan.getNvFanIndex()
	pwm = min(pwm, 100) // nvml speeds are in percent, so <= 100
	ret := fan.device.SetFanSpeed_v2(fanIdx, pwm)
	return nvError(ret)
}

func (fan *NvidiaFan) GetFanRpmCurveData() *map[int]float64 {
	return fan.FanCurveData
}

func (fan *NvidiaFan) AttachFanRpmCurveData(curveData *map[int]float64) (err error) {
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

func (fan *NvidiaFan) GetControlMode() (ControlMode, error) {
	fanIdx := fan.getNvFanIndex()
	policy, err := fan.device.GetFanControlPolicy_v2(fanIdx)
	if err != nvml.SUCCESS {
		return ControlModeUnknown, nvError(err)
	}
	ctrlMode := ControlModeAutomatic // "hardware auto pwm control" as default assumption
	if policy == nvml.FAN_POLICY_MANUAL {
		if fan.RunAtMaxSpeed {
			ctrlMode = ControlModeDisabled // "max speed" - "no control" in hwmon backend
		} else {
			ctrlMode = ControlModePWM // manual PWM control
		}
	}
	return ctrlMode, nil
}

func (fan *NvidiaFan) SetControlMode(value ControlMode) (err error) {
	device := fan.device
	fanIdx := fan.getNvFanIndex()
	var ret nvml.Return
	switch value {
	case ControlModeDisabled:
		fan.RunAtMaxSpeed = true
		ret = nvml.DeviceSetFanSpeed_v2(device, fanIdx, 100)
	case ControlModePWM:
		ret = device.SetFanControlPolicy(fanIdx, nvml.FAN_POLICY_MANUAL)
		// fan2go starts setting a curve-based PWM value very soon
		// after calling this, but out of an abundance of caution,
		// set a speed that makes the fans turn
		_ = fan.SetPwm(fan.GetStartPwm())
	case ControlModeUnknown:
		fallthrough // auto is a safe default
	case ControlModeAutomatic:
		ret = nvml.DeviceSetDefaultFanSpeed_v2(device, fanIdx)
	}
	return nvError(ret)
}

func (fan *NvidiaFan) GetConfig() configuration.FanConfig {
	return fan.Config
}

func (fan *NvidiaFan) Supports(feature FeatureFlag) bool {
	switch feature {
	case FeatureControlMode:
		return fan.CanSetControlMode
	case FeaturePwmSensor:
		// As of today, Nvidia's driver implementation returns a dynamic PWM value based on the
		// current RPM value of the fan (the current approximate measured fan speed in percent). This is not useful
		// for fan2go, because it expects the PWM value to reflect the value that has last been set. This is necessary
		// for the calculation of the setPwmToGetPwmMap, and the "pwmValueChangedByThirdParty" sanity check.
		// Since the value is not useful for fan2go, we ignore it, even though it technically exists and contains
		// actual data.
		return false
	case FeatureRpmSensor:
		return fan.CanReadRPM
	}
	return false
}
