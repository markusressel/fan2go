package configuration

type SensorConfig struct {
	// ID is the unique identifier for this sensor
	ID string `json:"id"`

	// Can be any of the following:
	HwMon *HwMonSensorConfig `json:"hwMon,omitempty"`
	File  *FileSensorConfig  `json:"file,omitempty"`
	Cmd   *CmdSensorConfig   `json:"cmd,omitempty"`
}

type HwMonSensorConfig struct {
	// Platform is the platform of the sensor as printed by 'fan2go detect'
	Platform string `json:"platform"`
	// Index is the index of the sensor as printed by 'fan2go detect'
	Index int `json:"index"`
	// TempInput is the sysfs path to the temperature input
	TempInput string
}

type FileSensorConfig struct {
	// Path is the sysfs path to the temperature input, content must be in milli-degrees
	Path string `json:"path"`
}

type CmdSensorConfig struct {
	// Exec is the command to execute
	Exec string `json:"exec"`
	// Args is a list of arguments to pass to the command
	Args []string `json:"args"`
}
