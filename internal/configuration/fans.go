package configuration

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
	PwmMap *map[int]int `json:"pwmMap,omitempty"`
	// Curve is the id of the speed curve associated with this fan.
	Curve string `json:"curve"`
	// By default speed values from the curve are scaled from 0..255 (or 0%..100%) to MinPwm..MaxPwm
	// before they're mapped with PwmMap (the value looked up in PwmMap is then used to actually
	// set the speed in the fan's controlling device).
	// If UseUnscaledCurveValues is set to true, the values from the curve for a specific temperature
	// are directly mapped with PwmMap, **without** scaling them first.
	// Note: If NeverStop is also set to true, values smaller than MinPwm are replaced with MinPwm
	UseUnscaledCurveValues bool `json:"useUnscaledCurveValues"`
	// Skip automatic detection/calculation of setPwmToGetPwmMap in fan initialization,
	// assume 1:1 mapping instead (user's pwmMap is still used, if it exists)
	SkipAutoPwmMap bool `json:"skipAutoPwmMap"`
	// ControlAlgorithm defines how the curve target is applied to the fan.
	ControlAlgorithm *ControlAlgorithmConfig `json:"controlAlgorithm,omitempty"`
	// If enabled, (re)sets the PWM mode to manual each cycle. Works around buggy BIOS and similar
	// that overwrites fan2go's settings. Disabled by default.
	AlwaysSetPwmMode bool `json:"alwaysSetPwmMode"`
	// SanityCheck defines Configuration options for sanity checks
	SanityCheck *SanityCheckConfig `json:"sanityCheck,omitempty"`
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
	// MaxPwmChangePerCycle defines the maximum change of the PWM value per cycle.
	MaxPwmChangePerCycle *int `json:"maxPwmChangePerCycle,omitempty"`
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
	PwmValueChangedByThirdParty *PwmValueChangedByThirdPartyConfig `json:"pwmValueChangedByThirdParty,omitempty"`
}

type PwmValueChangedByThirdPartyConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
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
}

type ControlLoopConfig struct {
	P float64 `json:"p"`
	I float64 `json:"i"`
	D float64 `json:"d"`
}
