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
	Name               string                  `json:"name"`
	Label              string                  `json:"label"`
	FilePath           string                  `json:"string"`
	Config             configuration.FanConfig `json:"configuration"`
	MovingAvg          float64                 `json:"moving_avg"`
	OriginalPwmEnabled int
}

func (fan FileFan) GetStartPwm() int {
	panic("implement me")
}

func (fan *FileFan) SetStartPwm(pwm int) {
	panic("implement me")
}

func (fan FileFan) GetMinPwm() int {
	panic("implement me")
}

func (fan *FileFan) SetMinPwm(pwm int) {
	panic("implement me")
}

func (fan FileFan) GetMaxPwm() int {
	panic("implement me")
}

func (fan *FileFan) SetMaxPwm(pwm int) {
	panic("implement me")
}

func (fan FileFan) GetRpm() int {
	panic("implement me")
}

func (fan FileFan) GetRpmAvg() float64 {
	panic("implement me")
}

func (fan *FileFan) SetRpmAvg(rpm float64) {
	panic("implement me")
}

func (fan FileFan) GetPwm() int {
	panic("implement me")
}

func (fan *FileFan) SetPwm(pwm int) (err error) {
	panic("implement me")
}

func (fan FileFan) GetFanCurveData() *map[int]*rolling.PointPolicy {
	panic("implement me")
}

func (fan *FileFan) SetFanCurveData(data *map[int]*rolling.PointPolicy) {
	panic("implement me")
}

func (fan FileFan) GetPwmEnabled() (int, error) {
	panic("implement me")
}

func (fan *FileFan) SetPwmEnabled(value int) (err error) {
	panic("implement me")
}

func (fan FileFan) IsPwmAuto() (bool, error) {
	panic("implement me")
}

func (fan FileFan) GetOriginalPwmEnabled() int {
	return fan.OriginalPwmEnabled
}

func (fan *FileFan) SetOriginalPwmEnabled(value int) {
	fan.OriginalPwmEnabled = value
}

func (fan FileFan) GetLastSetPwm() int {
	panic("implement me")
}

func (fan FileFan) GetId() string {
	return fan.Name
}

func (fan FileFan) GetName() string {
	return fan.Label
}

func (fan FileFan) GetConfig() configuration.FanConfig {
	return fan.Config
}

func (fan FileFan) GetValue() (result float64, err error) {
	filePath := fan.FilePath
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
	result = float64(integer)
	return result, err
}

func (fan FileFan) GetMovingAvg() (avg float64) {
	return fan.MovingAvg
}

func (fan *FileFan) SetMovingAvg(avg float64) {
	fan.MovingAvg = avg
}
