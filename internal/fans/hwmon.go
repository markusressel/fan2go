package fans

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/quirks"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

// writeSysfsInt writes pwm and pwm_enable of hwmon fans. Tests replace it to emulate drivers whose readback differs
// from the written value.
var writeSysfsInt = util.WriteIntToFile

type HwMonFan struct {
	Label        string                  `json:"label"`
	Index        int                     `json:"index"`
	RpmMovingAvg float64                 `json:"rpmMovingAvg"`
	Config       configuration.FanConfig `json:"config"`
	MinPwm       *int                    `json:"minPwm"`
	StartPwm     *int                    `json:"startPwm"`
	MaxPwm       *int                    `json:"maxPwm"`
	FanCurveData *map[int]float64        `json:"fanCurveData"`
	Rpm          int                     `json:"rpm"`
	Pwm          int                     `json:"pwm"`

	// lastKnownAutomaticControlMode stores the last known automatic control mode value (pwm_enable) for this fan.
	// This value is used when fan2go requests to set the control mode to automatic.
	lastKnownAutomaticControlMode *int

	// driverSpecificControlMode stores the pwm_enable value >= 3 that fan2go itself wrote last
	// (e.g. from an integer controlMode.active), so that reading it back doesn't overwrite
	// lastKnownAutomaticControlMode. nil if fan2go didn't write such a value.
	driverSpecificControlMode *int

	// pwmEnableReadable caches whether pwm_enable can be read (probed once on first Supports call).
	pwmEnableReadable *bool

	// manualModeUnconfirmed is true if fan2go took pwm_enable 0 with pwm 255 as manual mode, because the known
	// hardware quirks of the fan say that the driver reports it like that (see fullDutyReadsAsFullSpeed). The next
	// write of a pwm value below 255 confirms it: pwm_enable must read 1 then.
	manualModeUnconfirmed bool

	// mu protects all fields above, since the RPM monitor, the update loop of the fan controller,
	// the statistics collector and the REST API access the fan from different goroutines.
	// It is a leaf lock: no I/O and no calls to other fan methods while holding it.
	mu sync.RWMutex

	// knownQuirks is the entry of the known hardware quirks registry that applies to this fan (nil if none).
	// It is set on creation (from the config passed to NewFan) and never changed, so it is read without mu.
	// SetConfig doesn't change it.
	knownQuirks *quirks.Entry
}

// hwMonFanJson has the same fields and json tags as HwMonFan, without the mutex.
type hwMonFanJson struct {
	Label        string                  `json:"label"`
	Index        int                     `json:"index"`
	RpmMovingAvg float64                 `json:"rpmMovingAvg"`
	Config       configuration.FanConfig `json:"config"`
	MinPwm       *int                    `json:"minPwm"`
	StartPwm     *int                    `json:"startPwm"`
	MaxPwm       *int                    `json:"maxPwm"`
	FanCurveData *map[int]float64        `json:"fanCurveData"`
	Rpm          int                     `json:"rpm"`
	Pwm          int                     `json:"pwm"`
	Quirks       *knownQuirksJson        `json:"quirks,omitempty"`
}

// knownQuirksJson identifies the known hardware quirks entry applied to a fan in its JSON representation.
type knownQuirksJson struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// MarshalJSON serializes a snapshot of the fan that is taken under its lock.
func (fan *HwMonFan) MarshalJSON() ([]byte, error) {
	fan.mu.RLock()
	snapshot := hwMonFanJson{
		Label:        fan.Label,
		Index:        fan.Index,
		RpmMovingAvg: fan.RpmMovingAvg,
		Config:       fan.Config,
		MinPwm:       copyIntPtr(fan.MinPwm),
		StartPwm:     copyIntPtr(fan.StartPwm),
		MaxPwm:       copyIntPtr(fan.MaxPwm),
		FanCurveData: copyCurveData(fan.FanCurveData),
		Rpm:          fan.Rpm,
		Pwm:          fan.Pwm,
	}
	if fan.knownQuirks != nil {
		snapshot.Quirks = &knownQuirksJson{ID: fan.knownQuirks.ID, Version: fan.knownQuirks.Version}
	}
	fan.mu.RUnlock()
	return json.Marshal(snapshot)
}

// hwMonConfig returns the hwmon specific part of the fan config (sysfs paths).
func (fan *HwMonFan) hwMonConfig() configuration.HwMonFanConfig {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	if fan.Config.HwMon == nil {
		return configuration.HwMonFanConfig{}
	}
	return *fan.Config.HwMon
}

var _ KnownQuirksProvider = (*HwMonFan)(nil)

// KnownQuirks returns the known hardware quirks entry that applies to this fan, nil if none.
func (fan *HwMonFan) KnownQuirks() *quirks.Entry {
	return fan.knownQuirks
}

func (fan *HwMonFan) GetId() string {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config.ID
}

func (fan *HwMonFan) GetLabel() string {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Label
}

func (fan *HwMonFan) GetIndex() int {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Index
}

func (fan *HwMonFan) GetMinPwm() int {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	if fan.MinPwm != nil {
		return *fan.MinPwm
	}

	return MinPwmValue
}

func (fan *HwMonFan) SetMinPwm(pwm int, force bool) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	if fan.Config.MinPwm == nil || force {
		fan.MinPwm = &pwm
	}
}

func (fan *HwMonFan) GetStartPwm() int {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	if fan.StartPwm != nil {
		return *fan.StartPwm
	} else {
		return MaxPwmValue
	}
}

func (fan *HwMonFan) SetStartPwm(pwm int, force bool) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	if fan.Config.StartPwm == nil || force {
		fan.StartPwm = &pwm
	}
}

func (fan *HwMonFan) GetMaxPwm() int {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	if fan.MaxPwm != nil {
		return *fan.MaxPwm
	} else {
		return MaxPwmValue
	}
}

func (fan *HwMonFan) SetMaxPwm(pwm int, force bool) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	if fan.Config.MaxPwm == nil || force {
		fan.MaxPwm = &pwm
	}
}

func (fan *HwMonFan) GetRpm() (int, error) {
	if value, err := util.ReadIntFromFile(fan.hwMonConfig().RpmInputPath); err != nil {
		return 0, err
	} else {
		fan.mu.Lock()
		fan.Rpm = value
		fan.mu.Unlock()
		return value, nil
	}
}

func (fan *HwMonFan) GetRpmAvg() float64 {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.RpmMovingAvg
}

func (fan *HwMonFan) SetRpmAvg(rpm float64) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	fan.RpmMovingAvg = rpm
}

func (fan *HwMonFan) GetPwm() (int, error) {
	value, err := util.ReadIntFromFile(fan.hwMonConfig().PwmPath)
	if err != nil {
		return MinPwmValue, err
	}
	fan.mu.Lock()
	fan.Pwm = value
	fan.mu.Unlock()
	return value, nil
}

func (fan *HwMonFan) SetPwm(pwm int) (err error) {
	ui.Debug("Setting Fan PWM of '%s' to %d ...", fan.GetId(), pwm)
	err = writeSysfsInt(pwm, fan.hwMonConfig().PwmPath)
	if err != nil {
		return err
	}
	return fan.confirmManualMode(pwm)
}

// fullDutyReadsAsFullSpeed returns whether the known hardware quirks of the fan say that its pwm_enable reads 0
// instead of 1 while it is in manual mode at pwm 255 (e.g. it87 chips without fan on/off control).
// On other drivers, pwm_enable 0 can mean "full speed, no pwm control", so it is never treated as manual mode there.
func (fan *HwMonFan) fullDutyReadsAsFullSpeed() bool {
	if fan.knownQuirks == nil {
		return false
	}
	config := fan.hwMonConfig()
	return fan.knownQuirks.FullDutyReadsAsFullSpeed(config.HwmonName, config.PwmChannel)
}

// isManualModeAtFullDuty returns whether the given pwm_enable value means manual mode at pwm 255 on this fan:
// the value is 0, the known hardware quirks of the fan say that the driver reports manual mode at pwm 255 like that,
// and pwm reads 255. The driver derives this 0 from the pwm duty only, so the current pwm is checked, not the value
// fan2go wrote last. If it returns true, the next write of a pwm value below 255 must confirm it (see SetPwm).
func (fan *HwMonFan) isManualModeAtFullDuty(pwmEnableValue int) bool {
	if pwmEnableValue != 0 || !fan.fullDutyReadsAsFullSpeed() {
		return false
	}
	pwm, err := util.ReadIntFromFile(fan.hwMonConfig().PwmPath)
	if err != nil || pwm != MaxPwmValue {
		return false
	}
	fan.mu.Lock()
	fan.manualModeUnconfirmed = true
	fan.mu.Unlock()
	return true
}

// confirmManualMode checks that pwm_enable reads 1 after pwm was written, if fan2go took pwm_enable 0 with pwm 255 as
// manual mode before (see isManualModeAtFullDuty). It is checked once, at the first written value below 255: the
// driver reports 1 then, unless the fan isn't in manual mode. Returns an error if it isn't.
func (fan *HwMonFan) confirmManualMode(pwm int) error {
	if pwm >= MaxPwmValue {
		return nil
	}
	fan.mu.Lock()
	unconfirmed := fan.manualModeUnconfirmed
	fan.manualModeUnconfirmed = false
	fan.mu.Unlock()
	if !unconfirmed {
		return nil
	}
	value, err := fan.readPwmEnable()
	if err != nil {
		ui.Warning("Fan %s: cannot read pwm_enable to confirm manual mode: %v", fan.GetId(), err)
		return nil
	}
	if value != 1 {
		entryId, _ := fan.knownQuirks.Fingerprint()
		return fmt.Errorf("fan %s: pwm_enable read 0 at pwm 255, which is manual mode on this device "+
			"(known hardware quirk '%s'), but after writing pwm %d it reads %d instead of 1: PWM mode stuck to %d",
			fan.GetId(), entryId, pwm, value, value)
	}
	ui.Debug("Fan %s: pwm_enable reads 1 at pwm %d, manual mode confirmed", fan.GetId(), pwm)
	return nil
}

// GetFanRpmCurveData returns a copy of the fan curve data, so callers can read it
// while the fan is updated from another goroutine.
func (fan *HwMonFan) GetFanRpmCurveData() *map[int]float64 {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return copyCurveData(fan.FanCurveData)
}

// AttachFanCurveData attaches fan curve data from persistence to a fan
// Note: When the given data is incomplete, all values up until the highest
// value in the given dataset will be interpolated linearly
func (fan *HwMonFan) AttachFanRpmCurveData(curveData *map[int]float64) (err error) {
	fan.mu.Lock()
	if curveData == nil || len(*curveData) <= 0 {
		fan.FanCurveData = nil
		fan.mu.Unlock()
		return err
	}

	data := copyCurveData(curveData)
	fan.FanCurveData = data
	fan.mu.Unlock()

	applyPwmBoundariesFromCurveData(fan, *data)

	return err
}

func (fan *HwMonFan) UpdateFanRpmCurveValue(pwm int, rpm float64) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	if fan.FanCurveData == nil {
		fan.FanCurveData = &map[int]float64{}
	}
	(*fan.FanCurveData)[pwm] = rpm
}

func (fan *HwMonFan) GetCurveId() string {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config.Curve
}

func (fan *HwMonFan) ShouldNeverStop() bool {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config.NeverStop
}

func (fan *HwMonFan) GetControlMode() (ControlMode, error) {
	pwmEnabledValue, err := fan.readPwmEnable()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ui.Warning("Fan %s: pwm_enable file does not exist, assuming no control mode support", fan.GetId())
			return ControlModeDisabled, nil
		}
		return ControlModeUnknown, err
	}

	if pwmEnabledValue >= 2 {
		// if we have a pwm_enable value >= 2, store it as the last known automatic control mode
		// so that we can use it when setting the control mode to automatic later.
		// A driver specific mode that fan2go set itself is not the automatic mode to return to.
		fan.mu.Lock()
		if fan.driverSpecificControlMode == nil || *fan.driverSpecificControlMode != pwmEnabledValue {
			fan.lastKnownAutomaticControlMode = &pwmEnabledValue
		}
		fan.mu.Unlock()
		return ControlModeAutomatic, nil
	}

	switch pwmEnabledValue {
	case 0:
		if fan.isManualModeAtFullDuty(pwmEnabledValue) {
			// e.g. it87: manual mode at pwm 255 reads 0
			return ControlModePWM, nil
		}
		return ControlModeDisabled, nil
	case 1:
		return ControlModePWM, nil
	default:
		return ControlModeUnknown, fmt.Errorf("fan %s: cannot map pwm_enable value %d to ControlMode", fan.GetId(), pwmEnabledValue)
	}
}

// readPwmEnable reads the raw value of pwmX_enable, without mapping it to a ControlMode.
func (fan *HwMonFan) readPwmEnable() (int, error) {
	value, err := util.ReadIntFromFile(fan.hwMonConfig().PwmEnablePath)
	if err != nil {
		return 0, fmt.Errorf("fan %s: error reading pwm_enable: %w", fan.GetId(), err)
	}
	return value, nil
}

// SetControlMode writes the given value to pwmX_enable
//
// The values that are supported for pwmX_enable depend on the
// specific hardware and driver implementation.
//
// The most common arrangement is:
// 0 - no control (results in max speed)
// 1 - manual pwm control
// 2 - motherboard pwm control
//
// Note that not all drivers only use values in [0, 1, 2].
// F.ex. the "nct6775" driver, which is used for the "nct6798" chip uses:
// 0 - Fan control disabled (fans set to maximum speed)
// 1 - Manual mode, write to pwm[0-5] any value 0-255
// 2 - "Thermal Cruise" mode (set target temperature in pwm[1-7]_target_temp and pwm[1-7]_target_temp_tolerance)
// 3 - "Fan Speed Cruise" mode (set target fan speed with fan[1-7]_target and fan[1-7]_tolerance)
// 4 - "Smart Fan III" mode (NCT6775F only) (presumably similar to 5)
// 5 - "Smart Fan IV" mode (uses a configurable curve)
//
// Any value >= 2 read from pwmX_enable will be considered as "automatic control mode" by fan2go,
// and will be stored as the last known automatic control mode for this fan.
//
// ControlModeAutomatic (2) writes the last known automatic control mode (or 2 if none is known).
// Any other value >= 3 (e.g. from an integer `controlMode.active`) is written to pwmX_enable as is.
// ControlModeUnknown and other negative values are rejected without writing anything.
func (fan *HwMonFan) SetControlMode(value ControlMode) (err error) {
	if value < 0 {
		return fmt.Errorf("fan %s: cannot set unknown control mode %d", fan.GetId(), value)
	}
	var pwmEnabledValue int
	switch value {
	case ControlModeDisabled:
		pwmEnabledValue = 0
	case ControlModePWM:
		pwmEnabledValue = 1
	case ControlModeAutomatic:
		if lastKnownAutomaticControlMode := fan.GetLastKnownAutomaticControlMode(); lastKnownAutomaticControlMode != nil {
			// if we have a last known automatic control mode, use that
			pwmEnabledValue = *lastKnownAutomaticControlMode
		} else {
			// otherwise assume 2 as default for automatic control
			ui.Warning("No last known automatic control mode for fan '%s', assuming 2 (automatic control)", fan.GetId())
			pwmEnabledValue = 2
		}
	default:
		// driver specific mode (>= 3), write it as is
		pwmEnabledValue = int(value)
	}

	err = writeSysfsInt(pwmEnabledValue, fan.hwMonConfig().PwmEnablePath)
	if err != nil {
		return err
	}
	fan.mu.Lock()
	if value > ControlModeAutomatic {
		fan.driverSpecificControlMode = &pwmEnabledValue
	} else {
		fan.driverSpecificControlMode = nil
	}
	// set again below if manual mode at pwm 255 reads 0
	fan.manualModeUnconfirmed = false
	fan.mu.Unlock()
	currentValue, err := fan.readPwmEnable()
	if err != nil {
		// the write succeeded, only the validation is not possible
		ui.Warning("Cannot read pwm_enable of fan '%s', pwm_enable state validation cannot work. Continuing assuming it worked: %v", fan.GetId(), err)
		return nil
	}
	if value == ControlModeAutomatic {
		// the driver may report any of its automatic modes (>= 2)
		if currentValue < 2 {
			return fmt.Errorf("PWM mode stuck to %d", currentValue)
		}
		fan.mu.Lock()
		fan.lastKnownAutomaticControlMode = &currentValue
		fan.mu.Unlock()
		return nil
	}
	if currentValue != pwmEnabledValue {
		if value == ControlModePWM && fan.isManualModeAtFullDuty(currentValue) {
			ui.Debug("Fan %s: pwm_enable reads 0 at pwm 255, which is manual mode on this device (known hardware quirk)",
				fan.GetId())
			return nil
		}
		return fmt.Errorf("PWM mode stuck to %d", currentValue)
	}
	return nil
}

var _ AutomaticControlModeKeeper = (*HwMonFan)(nil)

// GetLastKnownAutomaticControlMode returns a copy of the last known automatic control mode (or nil).
func (fan *HwMonFan) GetLastKnownAutomaticControlMode() *int {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return copyIntPtr(fan.lastKnownAutomaticControlMode)
}

// SetLastKnownAutomaticControlMode sets the pwm_enable value to write for ControlModeAutomatic.
// Values < 2 are ignored, since they aren't automatic modes.
func (fan *HwMonFan) SetLastKnownAutomaticControlMode(value int) {
	if value < 2 {
		return
	}
	fan.mu.Lock()
	defer fan.mu.Unlock()
	fan.lastKnownAutomaticControlMode = &value
}

func (fan *HwMonFan) GetConfig() configuration.FanConfig {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config
}

func (fan *HwMonFan) SetConfig(config configuration.FanConfig) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	fan.Config = config
}

func (fan *HwMonFan) DisableSanityCheck(check SanityCheck) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	disableSanityCheck(&fan.Config, check)
}

func (fan *HwMonFan) Supports(feature FeatureFlag) bool {
	config := fan.hwMonConfig()
	switch feature {
	case FeatureControlModeWrite:
		_, err := os.Stat(config.PwmEnablePath)
		return err == nil
	case FeatureControlModeRead:
		fan.mu.RLock()
		cached := copyBoolPtr(fan.pwmEnableReadable)
		fan.mu.RUnlock()
		if cached != nil {
			return *cached
		}
		_, err := util.ReadIntFromFile(config.PwmEnablePath)
		readable := err == nil
		fan.mu.Lock()
		fan.pwmEnableReadable = &readable
		fan.mu.Unlock()
		return readable
	case FeaturePwmSensor:
		_, err := util.ReadIntFromFile(config.PwmPath)
		return err == nil
	case FeatureRpmSensor:
		_, err := os.Stat(config.RpmInputPath)
		return err == nil
	}
	return false
}
