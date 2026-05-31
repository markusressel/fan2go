package fans

import (
	"fmt"
	"sort"

	cmap "github.com/orcaman/concurrent-map/v2"
	"github.com/qdm12/reprint"

	"github.com/markusressel/fan2go/internal/configuration"
)

const (
	MaxPwmValue = 255
	MinPwmValue = 0
)

type FeatureFlag int

const (
	FeaturePwmSensor        FeatureFlag = 0
	FeatureRpmSensor        FeatureFlag = 1
	FeatureControlModeWrite FeatureFlag = 2
	FeatureControlModeRead  FeatureFlag = 3
)

type ControlMode int

const (
	// ControlModeDisabled completely disables control, resulting in a 100% voltage/PWM signal output
	ControlModeDisabled ControlMode = 0
	// ControlModePWM enables manual, fixed speed control via setting the pwm value
	ControlModePWM ControlMode = 1
	// ControlModeAutomatic enables automatic control by the integrated control of the mainboard
	ControlModeAutomatic ControlMode = 2

	// ControlModeUnknown is used when the control mode cannot be determined
	ControlModeUnknown ControlMode = -1
)

var (
	fanMap = cmap.New[Fan]()
)

type Fan interface {
	GetId() string

	// GetMinPwm returns the lowest PWM value where the fans are still spinning, when spinning previously
	// (unless configured otherwise for this fan in fan2go.yaml)
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
	// returns os.ErrInvalid if curveData is void of any data
	AttachFanRpmCurveData(curveData *map[int]float64) (err error)
	// UpdateFanRpmCurveValue updates a single PWM -> RPM mapping value
	UpdateFanRpmCurveValue(pwm int, rpm float64)

	// GetCurveId returns the id of the speed curve associated with this fan
	GetCurveId() string

	// ShouldNeverStop indicated whether this fan should never stop rotating
	ShouldNeverStop() bool

	// GetControlMode returns the current ControlMode of this fan
	GetControlMode() (ControlMode, error)
	// SetControlMode sets the ControlMode of this fan
	SetControlMode(value ControlMode) (err error)

	GetConfig() configuration.FanConfig
	SetConfig(config configuration.FanConfig)

	GetLabel() string
	GetIndex() int

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

	if config.Nvidia != nil {
		return CreateNvidiaFan(config)
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

	if config.Acpi != nil {
		return &AcpiFan{
			Config: config,
		}, nil
	}

	return nil, fmt.Errorf("no matching fan type for fan: %s", config.ID)
}

const (
	// rpmNoiseThreshold is the minimum RPM a data point must report to be considered
	// evidence that the fan is actually spinning. Readings at or below this value
	// are treated as sensor noise / fan-stopped.
	rpmNoiseThreshold = 50.0
	// maxPwmRpmRatio is the fraction of the peak RPM that a data point must reach
	// for the corresponding PWM to qualify as maxPwm. Using the *highest* such PWM
	// (rather than the single peak) makes maxPwm robust against noise spikes.
	maxPwmRpmRatio = 0.95
)

func IsRpmLikelySpinning(rpm float64) bool {
	return rpm >= rpmNoiseThreshold
}

// ComputePwmBoundaries calculates the startPwm and maxPwm values for a fan based on its fan curve data.
//
// startPwm: the lowest PWM value where the measured RPM is at least rpmNoiseThreshold.
// maxPwm:   the highest PWM value where RPM is at least maxPwmRpmRatio (95%) of the observed peak.
//
// Using the highest qualifying PWM for maxPwm (rather than the single measurement with the greatest
// value) avoids noise spikes at low PWM values being mistaken for the true peak.
func ComputePwmBoundaries(fan Fan) (startPwm int, maxPwm int) {
	pwmRpmMap := fan.GetFanRpmCurveData()
	return ComputePwmBoundariesFromCurveData(*pwmRpmMap, fan.GetStartPwm())
}

// ComputePwmBoundariesFromCurveData calculates startPwm and maxPwm directly from curve data.
// userStartPwm allows enforcing an explicit start PWM (pass MaxPwmValue to disable override).
func ComputePwmBoundariesFromCurveData(pwmRpmMap map[int]float64, userStartPwm int) (startPwm int, maxPwm int) {
	startPwm = 255

	var keys []int
	for pwm := range pwmRpmMap {
		keys = append(keys, pwm)
	}
	sort.Ints(keys)

	// Pass 1: find startPwm and peak RPM.
	peakRpm := 0.0
	for _, pwm := range keys {
		avgRpm := pwmRpmMap[pwm]
		if avgRpm > peakRpm {
			peakRpm = avgRpm
		}
		if avgRpm >= rpmNoiseThreshold && pwm < startPwm {
			startPwm = pwm
		}
	}

	// Pass 2: find the highest PWM where RPM >= maxPwmRpmRatio * peakRpm.
	// Iterating in ascending order and always overwriting ensures the last
	// (= highest) qualifying PWM is retained.
	minQualifyingRpm := peakRpm * maxPwmRpmRatio
	maxPwm = -1
	for _, pwm := range keys {
		if pwmRpmMap[pwm] >= minQualifyingRpm {
			maxPwm = pwm
		}
	}
	if maxPwm < 0 {
		maxPwm = 255 // fallback: no qualifying data point found
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
