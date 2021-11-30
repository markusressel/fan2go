package fans

import (
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
	"os/user"
	"path/filepath"
	"strings"
)

type FileFan struct {
	ID        string
	Label     string
	FilePath  string
	Config    configuration.FanConfig
	MovingAvg float64
}

func (fan FileFan) GetId() string {
	return fan.ID
}

func (fan FileFan) GetStartPwm() int {
	return 0
}

func (fan *FileFan) SetStartPwm(pwm int) {
	panic("not supported")
}

func (fan FileFan) GetMinPwm() int {
	return MinPwmValue
}

func (fan *FileFan) SetMinPwm(pwm int) {
	panic("not supported")
}

func (fan FileFan) GetMaxPwm() int {
	return MaxPwmValue
}

func (fan *FileFan) SetMaxPwm(pwm int) {
	panic("not supported")
}

func (fan FileFan) GetRpm() int {
	return 0
}

func (fan FileFan) GetRpmAvg() float64 {
	return 0
}

func (fan *FileFan) SetRpmAvg(rpm float64) {
	panic("not supported")
}

func (fan FileFan) GetPwm() (result int) {
	filePath := fan.FilePath
	// resolve home dir path
	if strings.HasPrefix(filePath, "~") {
		currentUser, err := user.Current()
		if err != nil {
			return result
		}

		filePath = filepath.Join(currentUser.HomeDir, filePath[1:])
	}

	integer, err := util.ReadIntFromFile(filePath)
	if err != nil {
		return MinPwmValue
	}
	result = integer
	return result
}

func (fan *FileFan) SetPwm(pwm int) (err error) {
	filePath := fan.FilePath
	// resolve home dir path
	if strings.HasPrefix(filePath, "~") {
		currentUser, err := user.Current()
		if err != nil {
			return err
		}

		filePath = filepath.Join(currentUser.HomeDir, filePath[1:])
	}

	err = util.WriteIntToFile(pwm, filePath)
	return err
}

func (fan FileFan) GetFanCurveData() *map[int]*rolling.PointPolicy {
	panic("implement me")
}

func (fan *FileFan) AttachFanCurveData(curveData *map[int][]float64) (err error) {
	panic("implement me")
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

func (fan *FileFan) SetPwmEnabled(value int) (err error) {
	panic("implement me")
}

func (fan FileFan) IsPwmAuto() (bool, error) {
	panic("implement me")
}

func (fan FileFan) Supports(feature int) bool {
	switch feature {
	case FeatureRpmSensor:
		// TODO: maybe we could support this in the future
		return false
	}
	return false
}
