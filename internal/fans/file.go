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
	ID        string
	Label     string
	Config    configuration.FanConfig
	MovingAvg float64
}

func (fan FileFan) GetId() string {
	return fan.ID
}

func (fan FileFan) GetStartPwm() int {
	return 1
}

func (fan *FileFan) SetStartPwm(pwm int) {
	return
}

func (fan FileFan) GetMinPwm() int {
	return MinPwmValue
}

func (fan *FileFan) SetMinPwm(pwm int) {
	// not supported
	return
}

func (fan FileFan) GetMaxPwm() int {
	return MaxPwmValue
}

func (fan *FileFan) SetMaxPwm(pwm int) {
	// not supported
	return
}

func (fan FileFan) GetRpm() (int, error) {
	return 0, nil
}

func (fan FileFan) GetRpmAvg() float64 {
	return 0
}

func (fan *FileFan) SetRpmAvg(rpm float64) {
	// not supported
	return
}

func (fan FileFan) GetPwm() (result int, err error) {
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
	}
	return nil
}

var interpolated = util.InterpolateLinearly(&map[int]float64{0: 0, 255: 255}, 0, 255)

func (fan FileFan) GetFanCurveData() *map[int]float64 {
	return &interpolated
}

func (fan *FileFan) AttachFanCurveData(curveData *map[int]float64) (err error) {
	// not supported
	return
}

func (fan FileFan) GetCurveId() string {
	return fan.Config.Curve
}

func (fan FileFan) ShouldNeverStop() bool {
	return fan.Config.NeverStop
}

func (fan FileFan) GetPwmEnabled() (int, error) {
	return 1, nil
}

func (fan *FileFan) SetPwmEnabled(value ControlMode) (err error) {
	// nothing to do
	return nil
}

func (fan FileFan) IsPwmAuto() (bool, error) {
	return true, nil
}

func (fan FileFan) Supports(feature FeatureFlag) bool {
	switch feature {
	case FeatureControlMode:
		return false
	case FeatureRpmSensor:
		// TODO: maybe we could support this in the future
		return false
	}
	return false
}
