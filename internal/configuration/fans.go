package configuration

import (
	"time"
)

// PwmMapConfig selects how the internal [0..255] PWM range is mapped to hardware PWM values.
// Exactly one sub-config must be non-nil. If PwmMap is nil in FanConfig, autodetect is assumed.
type PwmMapConfig struct {
	Autodetect *PwmMapAutodetectConfig `json:"autodetect,omitempty"`
	Identity   *PwmMapIdentityConfig   `json:"identity,omitempty"`
	Linear     *PwmMapLinearConfig     `json:"linear,omitempty"`
	Values     *PwmMapValuesConfig     `json:"values,omitempty"`
}

// PwmMapAutodetectConfig selects automatic PWM map detection during fan initialization.
type PwmMapAutodetectConfig struct{}

// PwmMapIdentityConfig selects a 1:1 mapping (0→0, 1→1, ..., 255→255).
type PwmMapIdentityConfig struct{}

// PwmMapLinearConfig holds control points for linear interpolation.
// Keys and values must be in [0..255]; values must be strictly monotonically increasing.
type PwmMapLinearConfig map[int]int

// PwmMapValuesConfig holds control points for step interpolation.
// Keys and values must be in [0..255]; values must be strictly monotonically increasing.
type PwmMapValuesConfig map[int]int

// SetPwmToGetPwmMapConfig selects how fan2go determines the set→get PWM mapping.
// Exactly one sub-config must be non-nil. If nil in FanConfig, autodetect is assumed.
type SetPwmToGetPwmMapConfig struct {
	Autodetect *SetPwmToGetPwmMapAutodetectConfig `json:"autodetect,omitempty"`
	Identity   *SetPwmToGetPwmMapIdentityConfig   `json:"identity,omitempty"`
	Linear     *SetPwmToGetPwmMapLinearConfig     `json:"linear,omitempty"`
	Values     *SetPwmToGetPwmMapValuesConfig     `json:"values,omitempty"`
}

// SetPwmToGetPwmMapAutodetectConfig selects automatic set→get PWM detection during initialization.
type SetPwmToGetPwmMapAutodetectConfig struct{}

// SetPwmToGetPwmMapIdentityConfig assumes a 1:1 set→get mapping (X→X for all X in [0..255]).
type SetPwmToGetPwmMapIdentityConfig struct{}

// SetPwmToGetPwmMapLinearConfig holds control points for linear interpolation of set→get mapping.
// Keys and values must be in [0..255]; values must be strictly monotonically increasing.
type SetPwmToGetPwmMapLinearConfig map[int]int

// SetPwmToGetPwmMapValuesConfig holds control points for step interpolation of set→get mapping.
// Keys and values must be in [0..255]; values must be strictly monotonically increasing.
type SetPwmToGetPwmMapValuesConfig map[int]int

// ControlModeConfig groups active and exit control mode settings for a fan.
type ControlModeConfig struct {
	// Active is the control mode to set when fan2go takes control of the fan.
	// Accepts "pwm", "disabled", or an integer. Defaults to "pwm" (with "disabled" fallback).
	Active *ControlModeValue `json:"active,omitempty"`
	// OnExit configures what fan2go does to the fan when it exits.
	// If omitted, the original control mode is restored (default behavior).
	OnExit *OnExitConfig `json:"onExit,omitempty"`
}

// ControlModeValue represents a control mode as a string name or integer string.
// Accepts: "pwm" / "manual", "disabled", "auto" / "automatic", or integer ("0", "1", "2", ...).
type ControlModeValue string

// OnExitConfig configures what fan2go does to the fan on exit.
// Valid combinations:
//   - restore (alone): restore original control mode — default
//   - none (alone): do nothing, leave fan at last fan2go speed
//   - controlMode and/or speed: set explicit values on exit
type OnExitConfig struct {
	Restore     *OnExitRestoreConfig `json:"restore,omitempty"`
	None        *OnExitNoneConfig    `json:"none,omitempty"`
	ControlMode *ControlModeValue    `mapstructure:"mode" json:"mode,omitempty"`
	// Speed is a speed value in [0..255] that is mapped through the pwmMap of the fan before it is written.
	Speed *int `json:"speed,omitempty"`
}

// OnExitRestoreConfig restores the original control mode (default behavior).
type OnExitRestoreConfig struct{}

// OnExitNoneConfig skips all exit actions, leaving the fan at the last speed set by fan2go.
type OnExitNoneConfig struct{}

type FanConfig struct {
	// ID is the unique identifier for the fan.
	ID        string `json:"id"`
	NeverStop bool   `json:"neverStop"`
	// MinPwm defines the lowest PWM value where the fans are still spinning, when spinning previously
	MinPwm *int `json:"minPwm,omitempty"`
	// StartPwm defines the lowest PWM value where the fans are able to start spinning from a standstill
	StartPwm *int `json:"startPwm,omitempty"`
	// MaxPwm defines the highest PWM value that yields an RPM increase
	MaxPwm *int `json:"maxPwm,omitempty"`
	// PwmMap is used to adapt how the expected [0..255] range is applied to a fan.
	// Some fans have weird missing sections in their PWM range (e.g. 0, 1, 2, 3, 5, 6, 7, 8, 10, ...),
	// other fans only support a very limited set of PWM values (e.g. 0, 1, 2, 3).
	PwmMap *PwmMapConfig `json:"pwmMap,omitempty"`
	// SetPwmToGetPwmMap configures how fan2go determines the mapping from a set PWM value to the
	// value the fan hardware reports back. If omitted, fan2go auto-detects this during initialization.
	SetPwmToGetPwmMap *SetPwmToGetPwmMapConfig `json:"setPwmToGetPwmMap,omitempty"`
	// ControlMode configures the control mode fan2go uses while active and on exit.
	ControlMode *ControlModeConfig `json:"controlMode,omitempty"`
	// Curve is the id of the speed curve associated with this fan.
	Curve string `json:"curve"`
	// By default speed values from the curve are scaled from 1..255 (or 1%..100%) to MinPwm..MaxPwm
	// before they're mapped with PwmMap (the value looked up in PwmMap is then used to actually
	// set the speed in the fan's controlling device) - 0(%) is mapped to 0 (unless NeverStop is set).
	// If UseUnscaledCurveValues is set to true, the values from the curve for a specific temperature
	// are directly mapped with PwmMap, **without** scaling them first.
	// Note: If NeverStop is also set to true, values smaller than MinPwm (incl. 0) are replaced with MinPwm
	UseUnscaledCurveValues bool `json:"useUnscaledCurveValues"`
	// PwmSetDelay overrides the global fanController.pwmSetDelay for this fan.
	PwmSetDelay *time.Duration `json:"pwmSetDelay,omitempty"`
	// ControlAlgorithm defines how the curve target is applied to the fan.
	ControlAlgorithm *ControlAlgorithmConfig `json:"controlAlgorithm,omitempty"`
	// SanityCheck defines Configuration options for sanity checks
	SanityCheck SanityCheckConfig `json:"sanityCheck"`
	// OnSensorFailure defines what fan2go does with the fan while its curve can't be evaluated
	// (e.g. because a sensor can't be read or its value is stale). Defaults to max.
	OnSensorFailure OnSensorFailureAction `json:"onSensorFailure,omitempty"`
	// HwMon, File and Cmd are the different ways to configure the respective fan types.
	HwMon  *HwMonFanConfig  `json:"hwMon,omitempty"`
	Nvidia *NvidiaFanConfig `json:"nvidia,omitempty"`
	File   *FileFanConfig   `json:"file,omitempty"`
	Cmd    *CmdFanConfig    `json:"cmd,omitempty"`

	// ControlLoop is a configuration for a PID control loop.
	//
	// Deprecated: ControlLoop exists for historical compatibility
	// and should not be used. To change how the fan is controlled
	// use ControlAlgorithm instead.
	ControlLoop *ControlLoopConfig `json:"controlLoop,omitempty"`
}

// OnSensorFailureAction is what fan2go does with a fan while its curve can't be evaluated.
type OnSensorFailureAction string

const (
	// OnSensorFailureMax drives the fan to its maxPwm (the default).
	OnSensorFailureMax OnSensorFailureAction = "max"
	// OnSensorFailureRestore hands the fan back like on exit (see controlMode.onExit).
	OnSensorFailureRestore OnSensorFailureAction = "restore"
)

// GetOnSensorFailure returns the configured onSensorFailure action, or OnSensorFailureMax if it is not set.
func (c FanConfig) GetOnSensorFailure() OnSensorFailureAction {
	if c.OnSensorFailure == "" {
		return OnSensorFailureMax
	}
	return c.OnSensorFailure
}

// GetControlAlgorithm returns the configured control algorithm (pid takes precedence if both are set,
// which the validation rejects), or DefaultControlAlgorithmConfig if none is configured.
// Exactly one of Pid and Direct is set in the result. Doesn't look at the deprecated ControlLoop,
// which takes precedence over ControlAlgorithm.
func (c FanConfig) GetControlAlgorithm() ControlAlgorithmConfig {
	if c.ControlAlgorithm != nil {
		if c.ControlAlgorithm.Pid != nil {
			return ControlAlgorithmConfig{Pid: c.ControlAlgorithm.Pid}
		}
		if c.ControlAlgorithm.Direct != nil {
			return ControlAlgorithmConfig{Direct: c.ControlAlgorithm.Direct}
		}
	}
	return DefaultControlAlgorithmConfig()
}

type ControlAlgorithm string

const (
	Pid    ControlAlgorithm = "pid"
	Direct ControlAlgorithm = "direct"
)

type ControlAlgorithmConfig struct {
	Direct *DirectControlAlgorithmConfig `json:"direct,omitempty"`
	Pid    *PidControlAlgorithmConfig    `json:"pid,omitempty"`
}

type DirectControlAlgorithmConfig struct {
	// MaxPwmChangePerCycle defines the maximum change of the curve value per cycle.
	//
	// Deprecated: depends on fanController.adjustmentTickRate, use RampUp and RampDown instead.
	MaxPwmChangePerCycle *int `json:"maxPwmChangePerCycle,omitempty"`
	// TimeConstant is the time constant of a first-order low-pass filter on the output (0 = off).
	// A plain number is seconds.
	TimeConstant SecondsOrDuration `json:"timeConstant,omitempty"`
	// RampUp is the maximum increase of the output per second, in curve units (0 = unlimited).
	RampUp CurveUnits `json:"rampUp,omitempty"`
	// RampDown is the maximum decrease of the output per second, in curve units (0 = unlimited).
	RampDown CurveUnits `json:"rampDown,omitempty"`
	// Deadband is the smallest change of the output that is applied, in curve units (0 = off).
	// Changes that reach 0 or 255, or that start or stop the fan, are always applied.
	Deadband CurveUnits `json:"deadband,omitempty"`
}

const (
	// DefaultDirectTimeConstant is the timeConstant of the default control algorithm
	DefaultDirectTimeConstant = SecondsOrDuration(2 * time.Second)
	// DefaultDirectDeadband is the deadband of the default control algorithm: 1%
	DefaultDirectDeadband CurveUnits = 2.55
)

// DefaultControlAlgorithmConfig returns the control algorithm of a fan without controlAlgorithm (and without
// the deprecated controlLoop): direct with a time constant of 2s and a deadband of 1%.
func DefaultControlAlgorithmConfig() ControlAlgorithmConfig {
	return ControlAlgorithmConfig{
		Direct: &DirectControlAlgorithmConfig{
			TimeConstant: DefaultDirectTimeConstant,
			Deadband:     DefaultDirectDeadband,
		},
	}
}

type PidControlAlgorithmConfig struct {
	// P is the proportional gain.
	P float64 `json:"p"`
	// I is the integral gain.
	I float64 `json:"i"`
	// D is the derivative gain.
	D float64 `json:"d"`
}

type SanityCheckConfig struct {
	// Enabled defines whether the sanity check is enabled.
	PwmValueChangedByThirdParty PwmValueChangedByThirdPartyConfig `json:"pwmValueChangedByThirdParty,omitempty"`
	FanModeChangedByThirdParty  FanModeChangedByThirdPartyConfig  `json:"fanModeChangedByThirdParty,omitempty"`
}

type PwmValueChangedByThirdPartyConfig struct {
	Enabled DefaultTrueBool `json:"enabled,omitempty"`
}

// FanModeChangedByThirdPartyConfig (re)sets the PWM mode to manual each cycle.
// Can be used to work around buggy BIOS and similar that overwrites fan2go's settings.
// Disabled by default.
type FanModeChangedByThirdPartyConfig struct {
	// Enabled defines whether the check is enabled.
	Enabled DefaultTrueBool `json:"enabled,omitempty"`

	// ThrottleDuration defines a duration to wait after each sanity check execution.
	// This is used to avoid bombarding the system with mode writes in a very short amount of time.
	ThrottleDuration time.Duration `json:"throttleDuration,omitempty" default:"10s"`
}

type HwMonFanConfig struct {
	Platform      string `json:"platform"`
	Index         int    `json:"index"`
	RpmChannel    int    `json:"rpmChannel"`
	PwmChannel    int    `json:"pwmChannel"`
	SysfsPath     string
	RpmInputPath  string
	PwmPath       string
	PwmEnablePath string
}

type NvidiaFanConfig struct {
	Device string `json:"device"` // e.g. "nvidia-10DE2489-0800"
	Index  int    `json:"index"`
}

type FileFanConfig struct {
	// Path is the sysfs path to the PWM output/input
	Path string `json:"path"`
	// PwmPath is the sysfs path to the PWM input
	RpmPath string `json:"rpmPath"`
}

type CmdFanConfig struct {
	// SetPwm is the command to set a PWM value
	SetPwm *ExecConfig `json:"setPwm,omitempty"`
	// GetPwm is the command to get the current PWM value
	GetPwm *ExecConfig `json:"getPwm,omitempty"`
	// GetRpm is the command to get the current RPM value
	GetRpm *ExecConfig `json:"getRpm,omitempty"`
}

type ExecConfig struct {
	// Exec is the command to execute
	Exec string `json:"exec"`
	// Args is a list of arguments to pass to the command
	Args []string `json:"args"`
	// Timeout is the maximum time the command may run (0 means DefaultCmdTimeout)
	Timeout time.Duration `json:"timeout,omitempty"`
}

// GetTimeout returns the configured timeout, or DefaultCmdTimeout if none is set.
func (c *ExecConfig) GetTimeout() time.Duration {
	return cmdTimeoutOrDefault(c.Timeout)
}

type ControlLoopConfig struct {
	P float64 `json:"p"`
	I float64 `json:"i"`
	D float64 `json:"d"`
}
