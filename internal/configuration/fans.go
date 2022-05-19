package configuration

type FanConfig struct {
	ID          string             `json:"id"`
	NeverStop   bool               `json:"neverStop"`
	StartPwm    *int               `json:"startPwm,omitempty"`
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
