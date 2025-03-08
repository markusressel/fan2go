package configuration

type FanConfig struct {
	ID        string `json:"id"`
	NeverStop bool   `json:"neverStop"`
	// MinPwm defines the lowest PWM value where the fans are still spinning, when spinning previously
	MinPwm *int `json:"minPwm,omitempty"`
	// StartPwm defines the lowest PWM value where the fans are able to start spinning from a standstill
	StartPwm *int `json:"startPwm,omitempty"`
	// MaxPwm defines the highest PWM value that yields an RPM increase
	MaxPwm           *int                    `json:"maxPwm,omitempty"`
	PwmMap           *map[int]int            `json:"pwmMap,omitempty"`
	Curve            string                  `json:"curve"`
	ControlAlgorithm *ControlAlgorithmConfig `json:"controlAlgorithm,omitempty"`
	HwMon            *HwMonFanConfig         `json:"hwMon,omitempty"`
	File             *FileFanConfig          `json:"file,omitempty"`
	Cmd              *CmdFanConfig           `json:"cmd,omitempty"`

	// ControlLoop is a configuration for a PID control loop.
	//
	// Deprecated: HeaderMap exists for historical compatibility
	// and should not be used. To access the headers returned by a handler,
	// use the Response.Header map as returned by the Result method.
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
	MaxPwmChangePerCycle *int `json:"maxPwmChangePerCycle,omitempty"`
}

type PidControlAlgorithmConfig struct {
	P float64 `json:"p"`
	I float64 `json:"i"`
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
	Path    string `json:"path"`
	RpmPath string `json:"rpmPath"`
}

type CmdFanConfig struct {
	SetPwm *ExecConfig `json:"setPwm,omitempty"`
	GetPwm *ExecConfig `json:"getPwm,omitempty"`
	GetRpm *ExecConfig `json:"getRpm,omitempty"`
}

type ExecConfig struct {
	Exec string   `json:"exec"`
	Args []string `json:"args"`
}

type ControlLoopConfig struct {
	P float64 `json:"p"`
	I float64 `json:"i"`
	D float64 `json:"d"`
}
