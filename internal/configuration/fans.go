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
	MaxPwm           *int                    `json:"maxPwm,omitempty"`
	PwmMap           *map[int]int            `json:"pwmMap,omitempty"`
	BypassPwmMap     *bool                   `json:"bypassPwmMap,omitempty"`
	Curve            string                  `json:"curve"`
	ControlAlgorithm *ControlAlgorithmConfig `json:"controlAlgorithm,omitempty"`
	HwMon            *HwMonFanConfig         `json:"hwMon,omitempty"`
	File             *FileFanConfig          `json:"file,omitempty"`
	Cmd              *CmdFanConfig           `json:"cmd,omitempty"`

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
