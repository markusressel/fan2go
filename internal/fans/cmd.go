package fans

import (
	"errors"
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"strconv"
	"strings"
	"time"
)

type CmdFan struct {
	ID        string                  `json:"id"`
	Config    configuration.FanConfig `json:"configuration"`
	MovingAvg float64                 `json:"movingAvg"`
}

func (fan CmdFan) GetId() string {
	return fan.ID
}

func (fan CmdFan) GetStartPwm() int {
	return 1
}

func (fan *CmdFan) SetStartPwm(pwm int) {
	return
}

func (fan CmdFan) GetMinPwm() int {
	return MinPwmValue
}

func (fan *CmdFan) SetMinPwm(pwm int) {
	// not supported
	return
}

func (fan CmdFan) GetMaxPwm() int {
	return MaxPwmValue
}

func (fan *CmdFan) SetMaxPwm(pwm int) {
	// not supported
	return
}

func (fan CmdFan) GetRpm() (int, error) {
	if !fan.Supports(FeatureRpmSensor) {
		return 0, nil
	}

	conf := fan.Config.Cmd.RpmGet

	timeout := 2 * time.Second
	result, err := util.SafeCmdExecution(conf.Exec, conf.Args, timeout)
	if err != nil {
		return 0, errors.New(fmt.Sprintf("Fan %s: %s", fan.GetId(), err.Error()))
	}

	temp, err := strconv.ParseFloat(result, 64)
	if err != nil {
		ui.Warning("Fan %s: Unable to read int from command output: %s", fan.GetId(), conf.Exec)
		return 0, err
	}

	return int(temp), nil
}

func (fan CmdFan) GetRpmAvg() float64 {
	return 0
}

func (fan *CmdFan) SetRpmAvg(rpm float64) {
	// not supported
	return
}

func (fan CmdFan) GetPwm() (result int, err error) {
	conf := fan.Config.Cmd.PwmGet

	timeout := 2 * time.Second
	output, err := util.SafeCmdExecution(conf.Exec, conf.Args, timeout)
	if err != nil {
		return 0, errors.New(fmt.Sprintf("Fan %s: %s", fan.GetId(), err.Error()))
	}

	pwm, err := strconv.ParseFloat(output, 64)
	if err != nil {
		ui.Warning("Fan %s: Unable to read int from command output: %s", fan.GetId(), conf.Exec)
		return 0, err
	}

	return int(pwm), nil
}

func (fan *CmdFan) SetPwm(pwm int) (err error) {
	conf := fan.Config.Cmd.PwmSet

	var args = []string{}
	for _, arg := range conf.Args {
		replaced := strings.ReplaceAll(arg, "%pwm%", strconv.Itoa(pwm))
		args = append(args, replaced)
	}

	timeout := 2 * time.Second
	_, err = util.SafeCmdExecution(conf.Exec, args, timeout)
	if err != nil {
		return errors.New(fmt.Sprintf("Fan %s: %s", fan.GetId(), err.Error()))
	}

	return nil
}

func (fan CmdFan) GetFanCurveData() *map[int]float64 {
	return &interpolated
}

func (fan *CmdFan) AttachFanCurveData(curveData *map[int]float64) (err error) {
	// not supported
	return
}

func (fan CmdFan) GetCurveId() string {
	return fan.Config.Curve
}

func (fan CmdFan) ShouldNeverStop() bool {
	return fan.Config.NeverStop
}

func (fan CmdFan) GetPwmEnabled() (int, error) {
	return 1, nil
}

func (fan *CmdFan) SetPwmEnabled(value ControlMode) (err error) {
	// nothing to do
	return nil
}

func (fan CmdFan) IsPwmAuto() (bool, error) {
	return true, nil
}

func (fan CmdFan) Supports(feature FeatureFlag) bool {
	switch feature {
	case FeatureRpmSensor:
		return fan.Config.Cmd.RpmGet != nil
	}
	return false
}