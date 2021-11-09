package configuration

type FanConfig struct {
	Id        string                 `json:"id"`
	Type      string                 `json:"type"`
	Params    map[string]interface{} `json:"params"`
	NeverStop bool                   `json:"neverstop"`
	Curve     string                 `json:"curve"`
}

const (
	FanTypeHwMon = "hwmon"
	FanTypeFile  = "file"
)

type HwMonFanParams struct {
	Platform string `json:"platform"`
	Index    int    `json:"index"`
}
