package configuration

type SensorConfig struct {
	// ID is the unique identifier for this sensor
	ID string `json:"id"`

	// Can be any of the following:
	HwMon  *HwMonSensorConfig  `json:"hwMon,omitempty"`
	Nvidia *NvidiaSensorConfig `json:"nvidia,omitempty"`
	File   *FileSensorConfig   `json:"file,omitempty"`
	Cmd    *CmdSensorConfig    `json:"cmd,omitempty"`
	Disk   *DiskSensorConfig   `json:"disk,omitempty"`
	Acpi   *AcpiSensorConfig   `json:"acpi,omitempty"`
}

type HwMonSensorConfig struct {
	// Platform is the platform of the sensor as printed by 'fan2go detect'
	Platform string `json:"platform"`
	// Index is the enumeration index of the sensor as printed by 'fan2go detect' (deprecated: prefer Channel)
	Index int `json:"index"`
	// Channel is the hardware channel number of the sensor (e.g. temp3_input → channel 3)
	Channel int `json:"channel"`
	// TempInput is the sysfs path to the temperature input
	TempInput string
}

type NvidiaSensorConfig struct {
	Device string `json:"device"` // e.g. "nvidia-10DE2489-0800"
	Index  int    `json:"index"`
	// Note: at least currently nvml only supports one temperature sensor per device
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

type DiskSensorConfig struct {
	// Device is the path to the block device. Accepts stable paths like /dev/disk/by-id/...
	// as well as plain paths like /dev/sda or just "sda".
	Device string `json:"device"`
}

// AcpiSensorConversion defines how the raw ACPI result value is interpreted.
type AcpiSensorConversion string

const (
	// AcpiSensorConversionCelsius multiplies the result by 1000 to convert °C to millidegrees (default).
	AcpiSensorConversionCelsius AcpiSensorConversion = "celsius"
	// AcpiSensorConversionMillicelsius passes the result through unchanged (already in millidegrees).
	AcpiSensorConversionMillicelsius AcpiSensorConversion = "millicelsius"
	// AcpiSensorConversionRaw passes the result through unchanged (non-temperature sensors).
	AcpiSensorConversionRaw AcpiSensorConversion = "raw"
)

type AcpiSensorConfig struct {
	// Method is the ACPI method path, e.g. `\_SB.AMW3.WMAX`
	Method string `json:"method"`
	// Args is the argument string to pass to the ACPI method, e.g. `0 0x13 {1, 0x04, 0x00, 0x00}`
	Args string `json:"args,omitempty"`
	// Conversion defines how the raw result is interpreted. Default: "celsius" (result * 1000).
	Conversion AcpiSensorConversion `json:"conversion,omitempty"`
}
