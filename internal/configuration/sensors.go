package configuration

type SensorConfig struct {
	ID    string             `json:"id"`
	HwMon *HwMonSensorConfig `json:"hwMon,omitempty"`
	File  *FileSensorConfig  `json:"file,omitempty"`
	Cmd   *CmdSensorConfig   `json:"cmd,omitempty"`
}

type HwMonSensorConfig struct {
	Platform  string `json:"platform"`
	Index     int    `json:"index"`
	TempInput string
}

type FileSensorConfig struct {
	Path string `json:"path"`
}

type CmdSensorConfig struct {
	Exec string   `json:"exec"`
	Args []string `json:"args"`
}
