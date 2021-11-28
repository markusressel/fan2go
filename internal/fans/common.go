package fans

import (
	"fmt"
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"os"
	"sort"
)

const (
	MaxPwmValue       = 255
	MinPwmValue       = 0
	InitialLastSetPwm = -10
)

var (
	FanMap = map[string]Fan{}
)

type Fan interface {
	GetId() string

	GetName() string

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
	GetFanCurveData() *map[int]*rolling.PointPolicy
	SetFanCurveData(data *map[int]*rolling.PointPolicy)
	// TODO: whats the difference?
	AttachFanCurveData(curveData *map[int][]float64) (err error)

	// GetCurveId returns the id of the speed curve associated with this fan
	GetCurveId() string

	// ShouldNeverStop indicated whether this fan should never stop rotating
	ShouldNeverStop() bool

	// GetPwmEnabled returns the current "pwm_enabled" value of this fan
	GetPwmEnabled() (int, error)
	SetPwmEnabled(value int) (err error)
	// IsPwmAuto indicates whether this fan is in "Auto" mode
	IsPwmAuto() (bool, error)

	SetOriginalPwmEnabled(int)
	// GetOriginalPwmEnabled  remembers the "pwm_enabled" state before fan2go took over control
	GetOriginalPwmEnabled() int
	// GetLastSetPwm remembers the last PWM value that has been set for this fan by fan2go
	GetLastSetPwm() int
}

func NewFan(config configuration.FanConfig) (Fan, error) {
	if config.HwMon != nil {
		return &HwMonFan{
			Name:         config.HwMon.Platform,
			Label:        config.ID,
			Index:        config.HwMon.Index,
			PwmOutput:    config.HwMon.PwmOutput,
			RpmInput:     config.HwMon.RpmInput,
			MinPwm:       MinPwmValue,
			MaxPwm:       MaxPwmValue,
			FanCurveData: &map[int]*rolling.PointPolicy{},
			LastSetPwm:   InitialLastSetPwm,
			Config:       config,
		}, nil
	}

	if config.File != nil {
		return &FileFan{}, nil
	}

	return nil, fmt.Errorf("no matching fan type for fan: %s", config.ID)
}

// AttachFanCurveData attaches fan curve data from persistence to a fan
// Note: When the given data is incomplete, all values up until the highest
// value in the given dataset will be interpolated linearly
// returns os.ErrInvalid if curveData is void of any data
func (fan *HwMonFan) AttachFanCurveData(curveData *map[int][]float64) (err error) {
	// convert the persisted map to arrays back to a moving window and attach it to the fan

	if curveData == nil || len(*curveData) <= 0 {
		ui.Error("Cant attach empty fan curve data to fan %s", fan.GetId())
		return os.ErrInvalid
	}

	const limit = 255
	var lastValueIdx int
	var lastValueAvg float64
	var nextValueIdx int
	var nextValueAvg float64
	for i := 0; i <= limit; i++ {
		fanCurveMovingWindow := util.CreateRollingWindow(configuration.CurrentConfig.RpmRollingWindowSize)

		pointValues, containsKey := (*curveData)[i]
		if containsKey && len(pointValues) > 0 {
			lastValueIdx = i
			lastValueAvg = util.Avg(pointValues)
		} else {
			if pointValues == nil {
				pointValues = []float64{lastValueAvg}
			}
			// find next value in curveData
			nextValueIdx = i
			for j := i; j <= limit; j++ {
				pointValues, containsKey := (*curveData)[j]
				if containsKey {
					nextValueIdx = j
					nextValueAvg = util.Avg(pointValues)
				}
			}
			if nextValueIdx == i {
				// we didn't find a next value in curveData, so we just copy the last point
				var valuesCopy = []float64{}
				copy(pointValues, valuesCopy)
				pointValues = valuesCopy
			} else {
				// interpolate average value to the next existing key
				ratio := util.Ratio(float64(i), float64(lastValueIdx), float64(nextValueIdx))
				interpolation := lastValueAvg + ratio*(nextValueAvg-lastValueAvg)
				pointValues = []float64{interpolation}
			}
		}

		var currentAvg float64
		for k := 0; k < configuration.CurrentConfig.RpmRollingWindowSize; k++ {
			var rpm float64

			if k < len(pointValues) {
				rpm = pointValues[k]
			} else {
				// fill the rolling window with averages if given values are not sufficient
				rpm = currentAvg
			}

			// update average
			if k == 0 {
				currentAvg = rpm
			} else {
				currentAvg = (currentAvg + rpm) / 2
			}

			// add value to window
			fanCurveMovingWindow.Append(rpm)
		}

		data := fan.GetFanCurveData()
		(*data)[i] = fanCurveMovingWindow
	}

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

	// get pwm keys that we have data for
	keys := make([]int, len(*pwmRpmMap))
	if pwmRpmMap == nil || len(keys) <= 0 {
		// we have no data yet
		startPwm = 0
	} else {
		i := 0
		for k := range *pwmRpmMap {
			keys[i] = k
			i++
		}
		// sort them increasing
		sort.Ints(keys)

		maxRpm := 0
		for _, pwm := range keys {
			window := (*pwmRpmMap)[pwm]
			avgRpm := int(util.GetWindowAvg(window))

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
