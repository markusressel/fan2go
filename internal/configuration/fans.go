package configuration

type FanConfig struct {
	ID        string          `json:"id"`
	NeverStop bool            `json:"neverStop"`
	Curve     string          `json:"curve"`
	HwMon     *HwMonFanConfig `json:"hwMon,omitempty"`
	File      *FileFanConfig  `json:"file,omitempty"`
}

type HwMonFanConfig struct {
	Platform string `json:"platform"`
	Index    int    `json:"index"`
}

type FileFanConfig struct {
	Path string `json:"path"`
}
