package configuration

type FanConfig struct {
	ID        string `json:"id"`
	NeverStop bool   `json:"neverStop"`
	// MinPwm defines the lowest PWM value where the fans are still spinning, when spinning previously
	MinPwm *int `json:"minPwm,omitempty"`
	// MinPwm defines the lowest PWM value where the fans are able to start spinning from a standstill
	StartPwm *int `json:"startPwm,omitempty"`
	// MaxPwm defines the highest PWM value that yields an RPM increase
	MaxPwm      *int               `json:"maxPwm,omitempty"`
	Curve       string             `json:"curve"`
	HwMon       *HwMonFanConfig    `json:"hwMon,omitempty"`
	File        *FileFanConfig     `json:"file,omitempty"`
	Cmd         *CmdFanConfig      `json:"cmd,omitempty"`
	ControlLoop *ControlLoopConfig `json:"controlLoop,omitempty"`
}

type HwMonFanConfig struct {
	Platform  string `json:"platform"`
	Index     int    `json:"index"`
	PwmOutput string
	RpmInput  string
}

type FileFanConfig struct {
	Path string `json:"path"`
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
