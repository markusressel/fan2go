package fans

import (
	"fmt"
	cmap "github.com/orcaman/concurrent-map/v2"
	"github.com/qdm12/reprint"
	"sort"

	"github.com/markusressel/fan2go/internal/configuration"
)

const (
	MaxPwmValue = 255
	MinPwmValue = 0
)

type FeatureFlag int

const (
	FeatureRpmSensor   FeatureFlag = 0
	FeatureControlMode FeatureFlag = 1
)

type ControlMode int

const (
	// ControlModeDisabled completely disables control, resulting in a 100% voltage/PWM signal output
	ControlModeDisabled ControlMode = 0
	// ControlModePWM enables manual, fixed speed control via setting the pwm value
	ControlModePWM ControlMode = 1
	// ControlModeAutomatic enables automatic control by the integrated control of the mainboard
	ControlModeAutomatic ControlMode = 2
)

var (
	fanMap = cmap.New[Fan]()
)

type Fan interface {
	GetId() string

	// GetMinPwm returns the lowest PWM value where the fans are still spinning, when spinning previously
	GetMinPwm() int
	SetMinPwm(pwm int, force bool)

	// GetStartPwm returns the min PWM at which the fan starts to rotate from a stand still
	GetStartPwm() int
	SetStartPwm(pwm int, force bool)

	// GetMaxPwm returns the highest PWM value that yields an RPM increase
	GetMaxPwm() int
	SetMaxPwm(pwm int, force bool)

	// GetRpm returns the current RPM value of this fan
	GetRpm() (int, error)
	GetRpmAvg() float64
	SetRpmAvg(rpm float64)

	// GetPwm returns the current PWM value of this fan
	GetPwm() (int, error)
	SetPwm(pwm int) (err error)

	// GetFanRpmCurveData returns the fan curve data for this fan
	GetFanRpmCurveData() *map[int]float64
	// AttachFanRpmCurveData attaches a complete set of PWM -> RPM mapping values to this fan
	AttachFanRpmCurveData(curveData *map[int]float64) (err error)
	// UpdateFanRpmCurveValue updates a single PWM -> RPM mapping value
	UpdateFanRpmCurveValue(pwm int, rpm float64)

	// GetCurveId returns the id of the speed curve associated with this fan
	GetCurveId() string

	// ShouldNeverStop indicated whether this fan should never stop rotating
	ShouldNeverStop() bool

	// GetPwmEnabled returns the current "pwm_enabled" value of this fan
	GetPwmEnabled() (int, error)
	SetPwmEnabled(value ControlMode) (err error)
	// IsPwmAuto indicates whether this fan is in "Auto" mode
	IsPwmAuto() (bool, error)

	Supports(feature FeatureFlag) bool
}

func NewFan(config configuration.FanConfig) (Fan, error) {
	if config.HwMon != nil {
		return &HwMonFan{
			Label:    config.ID,
			Index:    config.HwMon.Index,
			MinPwm:   config.MinPwm,
			StartPwm: config.StartPwm,
			MaxPwm:   config.MaxPwm,
			Config:   config,
		}, nil
	}

	if config.File != nil {
		return &FileFan{
			Config: config,
		}, nil
	}

	if config.Cmd != nil {
		return &CmdFan{
			Config: config,
		}, nil
	}

	return nil, fmt.Errorf("no matching fan type for fan: %s", config.ID)
}

// ComputePwmBoundaries calculates the startPwm and maxPwm values for a fan based on its fan curve data
func ComputePwmBoundaries(fan Fan) (startPwm int, maxPwm int) {
	userStartPwm := fan.GetStartPwm()
	startPwm = 255
	maxPwm = 255
	pwmRpmMap := fan.GetFanRpmCurveData()

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

	if userStartPwm < 255 {
		startPwm = userStartPwm
	}

	return startPwm, maxPwm
}

// RegisterFan registers a new fan
func RegisterFan(fan Fan) {
	fanMap.Set(fan.GetId(), fan)
}

// GetFan returns the fan with the given id
func GetFan(id string) (Fan, bool) {
	return fanMap.Get(id)
}

// SnapshotFanMap returns a snapshot of the current fan map
func SnapshotFanMap() map[string]Fan {
	return reprint.This(fanMap.Items()).(map[string]Fan)
}
