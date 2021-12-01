package fans

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"os"
	"sort"
)

const (
	MaxPwmValue = 255
	MinPwmValue = 0
)

const (
	FeatureRpmSensor = 0
)

var (
	FanMap = map[string]Fan{}
)

type Fan interface {
	GetId() string

	// GetStartPwm returns the min PWM at which the fan starts to rotate from a stand still
	GetStartPwm() int
	SetStartPwm(pwm int)

	// GetMinPwm returns the lowest PWM value where the fans are still spinning, when spinning previously
	GetMinPwm() int
	SetMinPwm(pwm int)

	// GetMaxPwm returns the highest PWM value that yields an RPM increase
	GetMaxPwm() int
	SetMaxPwm(pwm int)

	// GetRpm returns the current RPM value of this fan
	GetRpm() int
	GetRpmAvg() float64
	SetRpmAvg(rpm float64)

	// GetPwm returns the current PWM value of this fan
	GetPwm() int
	SetPwm(pwm int) (err error)

	// GetFanCurveData returns the fan curve data for this fan
	GetFanCurveData() *map[int]float64
	AttachFanCurveData(curveData *map[int]float64) (err error)

	// GetCurveId returns the id of the speed curve associated with this fan
	GetCurveId() string

	// ShouldNeverStop indicated whether this fan should never stop rotating
	ShouldNeverStop() bool

	// GetPwmEnabled returns the current "pwm_enabled" value of this fan
	GetPwmEnabled() (int, error)
	SetPwmEnabled(value int) (err error)
	// IsPwmAuto indicates whether this fan is in "Auto" mode
	IsPwmAuto() (bool, error)

	Supports(feature int) bool
}

func NewFan(config configuration.FanConfig) (Fan, error) {
	if config.HwMon != nil {
		return &HwMonFan{
			Label:     config.ID,
			Index:     config.HwMon.Index,
			PwmOutput: config.HwMon.PwmOutput,
			RpmInput:  config.HwMon.RpmInput,
			MinPwm:    MinPwmValue,
			MaxPwm:    MaxPwmValue,
			StartPwm:  config.StartPwm,
			Config:    config,
		}, nil
	}

	if config.File != nil {
		return &FileFan{
			FilePath: config.File.Path,
		}, nil
	}

	return nil, fmt.Errorf("no matching fan type for fan: %s", config.ID)
}

// AttachFanCurveData attaches fan curve data from persistence to a fan
// Note: When the given data is incomplete, all values up until the highest
// value in the given dataset will be interpolated linearly
// returns os.ErrInvalid if curveData is void of any data
func (fan *HwMonFan) AttachFanCurveData(curveData *map[int]float64) (err error) {
	if curveData == nil || len(*curveData) <= 0 {
		ui.Error("Cant attach empty fan curve data to fan %s", fan.GetId())
		return os.ErrInvalid
	}

	fan.FanCurveData = curveData

	startPwm, maxPwm := ComputePwmBoundaries(fan)

	fan.SetStartPwm(startPwm)
	fan.SetMaxPwm(maxPwm)

	// TODO: we don't have a way to determine this yet
	fan.SetMinPwm(startPwm)

	return err
}

// ComputePwmBoundaries calculates the startPwm and maxPwm values for a fan based on its fan curve data
func ComputePwmBoundaries(fan Fan) (startPwm int, maxPwm int) {
	startPwm = 255
	maxPwm = 255
	pwmRpmMap := fan.GetFanCurveData()

	// we have no data yet
	startPwm = 0

	if len(*pwmRpmMap) <= 0 {
		// we have no data yet
		startPwm = 0
	} else {

		var keys []int
		for pwm := range *pwmRpmMap {
			keys = append(keys, pwm)
		}
		sort.Ints(keys)

		maxRpm := 0
		for _, pwm := range keys {
			avgRpm := int((*pwmRpmMap)[pwm])
			if avgRpm > maxRpm {
				maxRpm = avgRpm
				maxPwm = pwm
			}

			if avgRpm > 0 && pwm < startPwm {
				startPwm = pwm
			}
		}
	}

	return startPwm, maxPwm
}
