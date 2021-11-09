package configuration

type SensorConfig struct {
	Id     string                 `json:"id"`
	Type   string                 `json:"type"`
	Params map[string]interface{} `json:"params"`
}

const (
	SensorTypeHwMon = "hwmon"
	SensorTypeFile  = "file"
)

type HwMonSensor struct {
	Platform string `json:"platform"`
	Index    int    `json:"index"`
}
