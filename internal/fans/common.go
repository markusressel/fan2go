package fans

import (
	"fmt"
	"sort"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/quirks"
	"github.com/markusressel/fan2go/internal/ui"
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

// SanityCheck identifies a sanity check of the fan controller, see configuration.SanityCheckConfig
type SanityCheck int

const (
	SanityCheckPwmValueChangedByThirdParty SanityCheck = iota
	SanityCheckFanModeChangedByThirdParty
)

// disableSanityCheck disables the given sanity check in the given config as a runtime override.
// The sanity check config is a value inside FanConfig (no pointers), so copies of the config
// handed out before are not affected.
func disableSanityCheck(config *configuration.FanConfig, check SanityCheck) {
	switch check {
	case SanityCheckPwmValueChangedByThirdParty:
		config.SanityCheck.PwmValueChangedByThirdParty.Enabled.SetOverride(false)
	case SanityCheckFanModeChangedByThirdParty:
		config.SanityCheck.FanModeChangedByThirdParty.Enabled.SetOverride(false)
	}
}

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
	// DisableSanityCheck disables the given sanity check in the config of this fan at runtime
	// (shown as a runtime override in the config). Safe to call while the fan is in use.
	DisableSanityCheck(check SanityCheck)

	GetLabel() string
	GetIndex() int

	Supports(feature FeatureFlag) bool
}

// KnownQuirksProvider is implemented by fans that can have an entry in the known hardware quirks registry
// (hwmon fans).
type KnownQuirksProvider interface {
	// KnownQuirks returns the registry entry that applies to the fan, nil if none.
	KnownQuirks() *quirks.Entry
}

// KnownQuirksOf returns the known hardware quirks entry that applies to the given fan, nil if none.
func KnownQuirksOf(fan Fan) *quirks.Entry {
	if provider, ok := fan.(KnownQuirksProvider); ok {
		return provider.KnownQuirks()
	}
	return nil
}

// AutomaticControlModeKeeper is implemented by fans whose driver has several automatic control modes
// (hwmon: every pwmX_enable value >= 2). SetControlMode(ControlModeAutomatic) writes the last known one.
type AutomaticControlModeKeeper interface {
	// GetLastKnownAutomaticControlMode returns the driver value of the last automatic control mode that was
	// read from (or written to) the fan, nil if none is known.
	GetLastKnownAutomaticControlMode() *int
	// SetLastKnownAutomaticControlMode sets the driver value to write for ControlModeAutomatic,
	// e.g. the one saved before fan2go took control of the fan in an earlier run.
	SetLastKnownAutomaticControlMode(value int)
}

func NewFan(config configuration.FanConfig) (Fan, error) {
	if config.HwMon != nil {
		return &HwMonFan{
			Label:       config.ID,
			Index:       config.HwMon.Index,
			MinPwm:      config.MinPwm,
			StartPwm:    config.StartPwm,
			MaxPwm:      config.MaxPwm,
			Config:      config,
			knownQuirks: quirks.ForFan(config),
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
	if pwmRpmMap == nil {
		// no curve data, e.g. because the fan has no RPM sensor
		pwmRpmMap = &map[int]float64{}
	}
	return ComputePwmBoundariesFromCurveData(*pwmRpmMap, configuredStartPwm(fan))
}

// applyPwmBoundariesFromCurveData sets the startPwm, maxPwm and minPwm of the fan that aren't configured
// from the given PWM -> RPM curve data, then resolves conflicts between derived and configured values
// (see clampDerivedPwmBoundaries).
func applyPwmBoundariesFromCurveData(fan Fan, curveData map[int]float64) {
	startPwm, maxPwm := ComputePwmBoundariesFromCurveData(curveData, configuredStartPwm(fan))
	fan.SetStartPwm(startPwm, false)
	fan.SetMaxPwm(maxPwm, false)

	// TODO: we don't have a way to determine this yet
	fan.SetMinPwm(startPwm, false)

	clampDerivedPwmBoundaries(fan)
}

// clampDerivedPwmBoundaries makes sure minPwm and startPwm aren't above maxPwm, which would invert the
// scaling of the curve value to [minPwm..maxPwm]. Validation rejects such configured values, so a conflict
// always involves a value derived from the curve data, e.g. a measured startPwm of 120 with maxPwm: 100.
// A configured value always wins: with a configured maxPwm, the derived minPwm/startPwm are lowered to it.
// Otherwise the derived maxPwm is raised to the (configured or derived) minPwm/startPwm, as far as the fan allows
// (a higher PWM is the safe direction), and only what is still above it is lowered. Configured values are never
// changed.
func clampDerivedPwmBoundaries(fan Fan) {
	config := fan.GetConfig()
	minPwm, startPwm, maxPwm := fan.GetMinPwm(), fan.GetStartPwm(), fan.GetMaxPwm()
	if minPwm <= maxPwm && startPwm <= maxPwm {
		return
	}

	if config.MaxPwm == nil {
		newMaxPwm := max(minPwm, startPwm)
		fan.SetMaxPwm(newMaxPwm, false)
		// the fan may limit the value (nvidia fans: 100)
		maxPwm = fan.GetMaxPwm()
		ui.Warning("Fan %s: the derived maxPwm is below minPwm (%d) or startPwm (%d), using maxPwm %d",
			fan.GetId(), minPwm, startPwm, maxPwm)
		if minPwm <= maxPwm && startPwm <= maxPwm {
			return
		}
	}

	if minPwm > maxPwm {
		if config.MinPwm == nil {
			ui.Warning("Fan %s: the derived minPwm (%d) is above maxPwm (%d), using minPwm %d",
				fan.GetId(), minPwm, maxPwm, maxPwm)
			fan.SetMinPwm(maxPwm, false)
		} else {
			ui.Warning("Fan %s: the configured minPwm (%d) is above the highest possible maxPwm (%d)",
				fan.GetId(), minPwm, maxPwm)
		}
	}
	if startPwm > maxPwm {
		if config.StartPwm == nil {
			ui.Warning("Fan %s: the derived startPwm (%d) is above maxPwm (%d), using startPwm %d",
				fan.GetId(), startPwm, maxPwm, maxPwm)
			fan.SetStartPwm(maxPwm, false)
		} else {
			ui.Warning("Fan %s: the configured startPwm (%d) is above the highest possible maxPwm (%d)",
				fan.GetId(), startPwm, maxPwm)
		}
	}
}

// configuredStartPwm returns the startPwm the user configured for the fan, or MaxPwmValue if there is none
// (which makes ComputePwmBoundariesFromCurveData use the measured value).
// Note: fan.GetStartPwm() can't be used for this, it returns the start PWM computed from the
// previously attached curve data.
func configuredStartPwm(fan Fan) int {
	if startPwm := fan.GetConfig().StartPwm; startPwm != nil {
		return *startPwm
	}
	return MaxPwmValue
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

func copyIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func copyBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

// copyCurveData returns a copy of the given fan curve data, or nil.
func copyCurveData(curveData *map[int]float64) *map[int]float64 {
	if curveData == nil {
		return nil
	}
	result := make(map[int]float64, len(*curveData))
	for pwm, rpm := range *curveData {
		result[pwm] = rpm
	}
	return &result
}
