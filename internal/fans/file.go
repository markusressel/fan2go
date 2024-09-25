package fans

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"os/user"
	"path/filepath"
	"strings"
)

type FileFan struct {
	Config    configuration.FanConfig `json:"config"`
	MovingAvg float64                 `json:"movingAvg"`

	Pwm int `json:"pwm"`
	Rpm int `json:"rpm"`
}

func (fan *FileFan) GetId() string {
	return fan.Config.ID
}

func (fan *FileFan) GetStartPwm() int {
	return 1
}

func (fan *FileFan) SetStartPwm(pwm int, force bool) {
}

func (fan *FileFan) GetMinPwm() int {
	return MinPwmValue
}

func (fan *FileFan) SetMinPwm(pwm int, force bool) {
	// not supported
}

func (fan *FileFan) GetMaxPwm() int {
	return MaxPwmValue
}

func (fan *FileFan) SetMaxPwm(pwm int, force bool) {
	// not supported
}

func (fan *FileFan) GetRpm() (result int, err error) {
	filePath := fan.Config.File.RpmPath
	// resolve home dir path
	if strings.HasPrefix(filePath, "~") {
		currentUser, err := user.Current()
		if err != nil {
			return result, err
		}

		filePath = filepath.Join(currentUser.HomeDir, filePath[1:])
	}

	integer, err := util.ReadIntFromFile(filePath)
	if err != nil {
		return 0, err
	}
	result = integer
	fan.Rpm = result
	return result, err
}

func (fan *FileFan) GetRpmAvg() float64 {
	return float64(fan.Rpm)
}

func (fan *FileFan) SetRpmAvg(rpm float64) {
	fan.Rpm = int(rpm)
}

func (fan *FileFan) GetPwm() (result int, err error) {
	filePath := fan.Config.File.Path
	// resolve home dir path
	if strings.HasPrefix(filePath, "~") {
		currentUser, err := user.Current()
		if err != nil {
			return result, err
		}

		filePath = filepath.Join(currentUser.HomeDir, filePath[1:])
	}

	integer, err := util.ReadIntFromFile(filePath)
	if err != nil {
		return MinPwmValue, err
	}
	result = integer
	fan.Pwm = result
	return result, err
}

func (fan *FileFan) SetPwm(pwm int) (err error) {
	filePath := fan.Config.File.Path
	// resolve home dir path
	if strings.HasPrefix(filePath, "~") {
		currentUser, err := user.Current()
		if err != nil {
			return err
		}

		filePath = filepath.Join(currentUser.HomeDir, filePath[1:])
	}

	err = util.WriteIntToFile(pwm, filePath)
	if err != nil {
		ui.Error("Unable to write to file: %v", fan.Config.File.Path)
		return err
	}
	return nil
}

var interpolated = util.InterpolateLinearly(&map[int]float64{0: 0, 255: 255}, 0, 255)

func (fan *FileFan) GetFanRpmCurveData() *map[int]float64 {
	return &interpolated
}

func (fan *FileFan) AttachFanRpmCurveData(curveData *map[int]float64) (err error) {
	// not supported
	return
}

func (fan *FileFan) UpdateFanRpmCurveValue(pwm int, rpm float64) {
	// not supported
}

func (fan *FileFan) GetCurveId() string {
	return fan.Config.Curve
}

func (fan *FileFan) ShouldNeverStop() bool {
	return fan.Config.NeverStop
}

func (fan *FileFan) GetPwmEnabled() (int, error) {
	return 1, nil
}

func (fan *FileFan) SetPwmEnabled(value ControlMode) (err error) {
	// nothing to do
	return nil
}

func (fan *FileFan) IsPwmAuto() (bool, error) {
	return true, nil
}

func (fan *FileFan) Supports(feature FeatureFlag) bool {
	switch feature {
	case FeatureControlMode:
		return false
	case FeatureRpmSensor:
		if len(fan.Config.File.RpmPath) > 0 {
			return true
		}
	}
	return false
}
