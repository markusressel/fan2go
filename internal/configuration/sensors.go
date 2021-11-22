package configuration

type SensorConfig struct {
	ID    string             `json:"id"`
	HwMon *HwMonSensorConfig `json:"hwMon,omitempty"`
	File  *FileSensorConfig  `json:"file,omitempty"`
}

type HwMonSensorConfig struct {
	Platform string `json:"platform"`
	Index    int    `json:"index"`
}

type FileSensorConfig struct {
	Path string `json:"path"`
}
