package fans

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
)

type AcpiFan struct {
	Config configuration.FanConfig `json:"config"`

	Rpm int `json:"rpm"`
	Pwm int `json:"pwm"`
}

func (fan *AcpiFan) GetId() string {
	return fan.Config.ID
}

func (fan *AcpiFan) GetLabel() string {
	return "ACPI Fan " + fan.Config.ID
}

func (fan *AcpiFan) GetIndex() int {
	return 1
}

func (fan *AcpiFan) GetStartPwm() int {
	return 1
}

func (fan *AcpiFan) SetStartPwm(pwm int, force bool) {
	// not supported
}

func (fan *AcpiFan) GetMinPwm() int {
	return MinPwmValue
}

func (fan *AcpiFan) SetMinPwm(pwm int, force bool) {
	// not supported
}

func (fan *AcpiFan) GetMaxPwm() int {
	return MaxPwmValue
}

func (fan *AcpiFan) SetMaxPwm(pwm int, force bool) {
	// not supported
}

func (fan *AcpiFan) GetRpm() (int, error) {
	if !fan.Supports(FeatureRpmSensor) {
		return 0, nil
	}
	return fan.getRpmAt(util.ExecuteAcpiCall)
}

func (fan *AcpiFan) getRpmAt(callFn func(method, args string) (int64, error)) (int, error) {
	conf := fan.Config.Acpi.GetRpm
	val, err := callFn(conf.Method, conf.Args)
	if err != nil {
		return 0, fmt.Errorf("fan %s getRpm: %w", fan.GetId(), err)
	}
	fan.Rpm = int(val)
	return int(val), nil
}

func (fan *AcpiFan) GetRpmAvg() float64 {
	return float64(fan.Rpm)
}

func (fan *AcpiFan) SetRpmAvg(rpm float64) {
	fan.Rpm = int(rpm)
}

func (fan *AcpiFan) GetPwm() (int, error) {
	if !fan.Supports(FeaturePwmSensor) {
		return fan.Pwm, nil
	}
	return fan.getPwmAt(util.ExecuteAcpiCall)
}

func (fan *AcpiFan) getPwmAt(callFn func(method, args string) (int64, error)) (int, error) {
	conf := fan.Config.Acpi.GetPwm
	val, err := callFn(conf.Method, conf.Args)
	if err != nil {
		return 0, fmt.Errorf("fan %s getPwm: %w", fan.GetId(), err)
	}

	var pwm int
	if conf.Conversion == configuration.AcpiFanConversionPercentage {
		pwm = int(math.Round(float64(val) * 255.0 / 100.0))
	} else {
		pwm = int(val)
	}

	fan.Pwm = pwm
	return pwm, nil
}

func (fan *AcpiFan) SetPwm(pwm int) error {
	return fan.setPwmAt(util.ExecuteAcpiCall, pwm)
}

func (fan *AcpiFan) setPwmAt(callFn func(method, args string) (int64, error), pwm int) error {
	conf := fan.Config.Acpi.SetPwm

	acpiVal := pwm
	if conf.Conversion == configuration.AcpiFanConversionPercentage {
		acpiVal = int(math.Round(float64(pwm) * 100.0 / 255.0))
	}

	args := strings.ReplaceAll(conf.Args, "%pwm%", strconv.Itoa(acpiVal))
	_, err := callFn(conf.Method, args)
	if err != nil {
		return fmt.Errorf("fan %s setPwm: %w", fan.GetId(), err)
	}

	fan.Pwm = pwm
	return nil
}

func (fan *AcpiFan) GetFanRpmCurveData() *map[int]float64 {
	return &interpolated
}

func (fan *AcpiFan) AttachFanRpmCurveData(curveData *map[int]float64) (err error) {
	// not supported
	return
}

func (fan *AcpiFan) UpdateFanRpmCurveValue(pwm int, rpm float64) {
	// not supported
}

func (fan *AcpiFan) GetCurveId() string {
	return fan.Config.Curve
}

func (fan *AcpiFan) ShouldNeverStop() bool {
	return fan.Config.NeverStop
}

func (fan *AcpiFan) GetControlMode() (ControlMode, error) {
	return ControlModePWM, nil
}

func (fan *AcpiFan) SetControlMode(value ControlMode) error {
	// not supported
	return nil
}

func (fan *AcpiFan) GetConfig() configuration.FanConfig {
	return fan.Config
}

func (fan *AcpiFan) SetConfig(config configuration.FanConfig) {
	fan.Config = config
}

func (fan *AcpiFan) Supports(feature FeatureFlag) bool {
	switch feature {
	case FeatureControlModeWrite:
		return false
	case FeatureControlModeRead:
		return false
	case FeaturePwmSensor:
		return fan.Config.Acpi.GetPwm != nil
	case FeatureRpmSensor:
		return fan.Config.Acpi.GetRpm != nil
	}
	return false
}
