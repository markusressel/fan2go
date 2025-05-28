package fans

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"strconv"
	"strings"
	"time"
)

type CmdFan struct {
	Config    configuration.FanConfig `json:"config"`
	MovingAvg float64                 `json:"movingAvg"`

	Rpm int `json:"rpm"`
	Pwm int `json:"pwm"`
}

func (fan *CmdFan) GetId() string {
	return fan.Config.ID
}

func (fan *CmdFan) GetStartPwm() int {
	return 1
}

func (fan *CmdFan) SetStartPwm(pwm int, force bool) {
}

func (fan *CmdFan) GetMinPwm() int {
	return MinPwmValue
}

func (fan *CmdFan) SetMinPwm(pwm int, force bool) {
	// not supported
}

func (fan *CmdFan) GetMaxPwm() int {
	return MaxPwmValue
}

func (fan *CmdFan) SetMaxPwm(pwm int, force bool) {
	// not supported
}

func (fan *CmdFan) GetRpm() (int, error) {
	if !fan.Supports(FeatureRpmSensor) {
		return 0, nil
	}

	conf := fan.Config.Cmd.GetRpm

	timeout := 2 * time.Second
	result, err := util.SafeCmdExecution(conf.Exec, conf.Args, timeout)
	if err != nil {
		return 0, err
	}

	rpm, err := strconv.ParseFloat(result, 64)
	if err != nil {
		ui.Warning("Unable to read int from command output: %s", conf.Exec)
		return 0, err
	}

	fan.Rpm = int(rpm)

	return int(rpm), nil
}

func (fan *CmdFan) GetRpmAvg() float64 {
	return float64(fan.Rpm)
}

func (fan *CmdFan) SetRpmAvg(rpm float64) {
	fan.Rpm = int(rpm)
}

func (fan *CmdFan) GetPwm() (result int, err error) {
	conf := fan.Config.Cmd.GetPwm

	timeout := 2 * time.Second
	output, err := util.SafeCmdExecution(conf.Exec, conf.Args, timeout)
	if err != nil {
		return 0, err
	}

	pwm, err := strconv.ParseFloat(output, 64)
	if err != nil {
		ui.Warning("Unable to read int from command output: %s", conf.Exec)
		return 0, err
	}

	fan.Pwm = int(pwm)

	return int(pwm), nil
}

func (fan *CmdFan) SetPwm(pwm int) (err error) {
	conf := fan.Config.Cmd.SetPwm

	var args = []string{}
	for _, arg := range conf.Args {
		replaced := strings.ReplaceAll(arg, "%pwm%", strconv.Itoa(pwm))
		args = append(args, replaced)
	}

	timeout := 2 * time.Second
	_, err = util.SafeCmdExecution(conf.Exec, args, timeout)
	if err != nil {
		return fmt.Errorf("%s", err.Error())
	}

	return nil
}

func (fan *CmdFan) GetFanRpmCurveData() *map[int]float64 {
	return &interpolated
}

func (fan *CmdFan) AttachFanRpmCurveData(curveData *map[int]float64) (err error) {
	// not supported
	return
}

func (fan *CmdFan) UpdateFanRpmCurveValue(pwm int, rpm float64) {
	// not supported
}

func (fan *CmdFan) GetCurveId() string {
	return fan.Config.Curve
}

func (fan *CmdFan) ShouldNeverStop() bool {
	return fan.Config.NeverStop
}

func (fan *CmdFan) GetPwmEnabled() (int, error) {
	return 1, nil
}

func (fan *CmdFan) SetPwmEnabled(value ControlMode) (err error) {
	// nothing to do
	return nil
}

func (fan *CmdFan) IsPwmAuto() (bool, error) {
	return true, nil
}

func (fan *CmdFan) GetConfig() configuration.FanConfig {
	return fan.Config
}

func (fan *CmdFan) Supports(feature FeatureFlag) bool {
	switch feature {
	case FeatureControlMode:
		return false
	case FeaturePwmSensor:
		return fan.Config.Cmd.GetPwm != nil
	case FeatureRpmSensor:
		return fan.Config.Cmd.GetRpm != nil
	}
	return false
}
