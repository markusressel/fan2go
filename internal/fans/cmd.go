package fans

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type CmdFan struct {
	Config    configuration.FanConfig `json:"config"`
	MovingAvg float64                 `json:"movingAvg"`

	Rpm int `json:"rpm"`
	Pwm int `json:"pwm"`
	// RpmMovingAvg is the moving average of Rpm, kept by the fan controller
	RpmMovingAvg float64 `json:"rpmMovingAvg"`

	// mu protects all fields above, since the fan is accessed from different goroutines.
	// It is a leaf lock: no command execution and no calls to other fan methods while holding it.
	mu sync.RWMutex
}

// cmdFanJson has the same fields and json tags as CmdFan, without the mutex.
type cmdFanJson struct {
	Config    configuration.FanConfig `json:"config"`
	MovingAvg float64                 `json:"movingAvg"`

	Rpm          int     `json:"rpm"`
	Pwm          int     `json:"pwm"`
	RpmMovingAvg float64 `json:"rpmMovingAvg"`
}

// MarshalJSON serializes a snapshot of the fan that is taken under its lock.
func (fan *CmdFan) MarshalJSON() ([]byte, error) {
	fan.mu.RLock()
	snapshot := cmdFanJson{
		Config:       fan.Config,
		MovingAvg:    fan.MovingAvg,
		Rpm:          fan.Rpm,
		Pwm:          fan.Pwm,
		RpmMovingAvg: fan.RpmMovingAvg,
	}
	fan.mu.RUnlock()
	return json.Marshal(snapshot)
}

// cmdConfig returns the cmd specific part of the fan config.
func (fan *CmdFan) cmdConfig() configuration.CmdFanConfig {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	if fan.Config.Cmd == nil {
		return configuration.CmdFanConfig{}
	}
	return *fan.Config.Cmd
}

func (fan *CmdFan) GetId() string {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config.ID
}

func (fan *CmdFan) GetLabel() string {
	return "CMD Fan " + fan.GetId()
}

func (fan *CmdFan) GetIndex() int {
	return 1
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

	conf := fan.cmdConfig().GetRpm

	result, err := util.SafeCmdExecution(conf.Exec, conf.Args, conf.GetTimeout())
	if err != nil {
		return 0, err
	}

	rpm, err := strconv.ParseFloat(result, 64)
	if err != nil {
		ui.Warning("Unable to read int from command output: %s", conf.Exec)
		return 0, err
	}

	fan.mu.Lock()
	fan.Rpm = int(rpm)
	fan.mu.Unlock()

	return int(rpm), nil
}

func (fan *CmdFan) GetRpmAvg() float64 {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.RpmMovingAvg
}

func (fan *CmdFan) SetRpmAvg(rpm float64) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	fan.RpmMovingAvg = rpm
}

func (fan *CmdFan) GetPwm() (result int, err error) {
	conf := fan.cmdConfig().GetPwm

	output, err := util.SafeCmdExecution(conf.Exec, conf.Args, conf.GetTimeout())
	if err != nil {
		return 0, err
	}

	pwm, err := strconv.ParseFloat(output, 64)
	if err != nil {
		ui.Warning("Unable to read int from command output: %s", conf.Exec)
		return 0, err
	}

	fan.mu.Lock()
	fan.Pwm = int(pwm)
	fan.mu.Unlock()

	return int(pwm), nil
}

func (fan *CmdFan) SetPwm(pwm int) (err error) {
	conf := fan.cmdConfig().SetPwm

	var args = []string{}
	for _, arg := range conf.Args {
		replaced := strings.ReplaceAll(arg, "%pwm%", strconv.Itoa(pwm))
		args = append(args, replaced)
	}

	_, err = util.SafeCmdExecution(conf.Exec, args, conf.GetTimeout())
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
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config.Curve
}

func (fan *CmdFan) ShouldNeverStop() bool {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config.NeverStop
}

func (fan *CmdFan) GetControlMode() (ControlMode, error) {
	return ControlModePWM, nil
}

func (fan *CmdFan) SetControlMode(value ControlMode) (err error) {
	// nothing to do
	return nil
}

func (fan *CmdFan) GetConfig() configuration.FanConfig {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config
}

func (fan *CmdFan) SetConfig(config configuration.FanConfig) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	fan.Config = config
}

func (fan *CmdFan) DisableSanityCheck(check SanityCheck) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	disableSanityCheck(&fan.Config, check)
}

func (fan *CmdFan) Supports(feature FeatureFlag) bool {
	switch feature {
	case FeatureControlModeWrite:
		return false
	case FeatureControlModeRead:
		return false
	case FeaturePwmSensor:
		return fan.cmdConfig().GetPwm != nil
	case FeatureRpmSensor:
		return fan.cmdConfig().GetRpm != nil
	}
	return false
}
