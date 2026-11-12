package hwmon

import (
	"fmt"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon/lmsensors"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/stretchr/testify/assert"
)

func TestComputeIdentifierIsa(t *testing.T) {
	// GIVEN
	c := lmsensors.Chip{
		Prefix: "ucsi_source_psy_USBC000:002",
		Addr:   0x0f1,
		Bus: lmsensors.Bus{
			Type: BusTypeIsa,
			Nr:   1,
		},
		Path: "/sys/class/hwmon/hwmon7",
	}
	expected := "ucsi_source_psy_USBC000:002-isa-10f1"

	// WHEN
	result := computeIdentifier(c)

	// THEN
	assert.Equal(t, expected, result)
}

func TestComputeIdentifierPci(t *testing.T) {
	// GIVEN
	c := lmsensors.Chip{
		Prefix: "nvme",
		Addr:   0x5,
		Bus: lmsensors.Bus{
			Type: BusTypePci,
			Nr:   1,
		},
		Path: "/sys/class/hwmon/hwmon4",
	}
	expected := "nvme-pci-1005"

	// WHEN
	result := computeIdentifier(c)

	// THEN
	assert.Equal(t, expected, result)
}

func TestComputeIdentifierAcpi(t *testing.T) {
	// GIVEN
	c := lmsensors.Chip{
		Prefix: "nvme",
		Bus: lmsensors.Bus{
			Type: BusTypeAcpi,
			Nr:   1,
		},
		Path: "/sys/class/hwmon/hwmon4",
	}
	expected := fmt.Sprintf("%s-acpi-%d", c.Prefix, c.Bus.Nr)

	// WHEN
	result := computeIdentifier(c)

	// THEN
	assert.Equal(t, expected, result)
}

func TestComputeIdentifierPciHighAddress(t *testing.T) {
	// GIVEN a PCI chip at an address >= 0x1000, which libsensors (`sensors`) prints as "nvme-pci-5a00".
	// libsensors uses bus number 0 for PCI chips.
	c := lmsensors.Chip{
		Prefix: "nvme",
		Addr:   0x5a00,
		Bus: lmsensors.Bus{
			Type: BusTypePci,
			Nr:   0,
		},
		Path: "/sys/class/hwmon/hwmon3",
	}

	// WHEN
	result := computeIdentifier(c)

	// THEN the fan2go name keeps its format (bus number followed by the address)
	assert.Equal(t, "nvme-pci-05a00", result)
}

func TestComputeIdentifierPciLowAddressMatchesLmSensorsFormat(t *testing.T) {
	// GIVEN a PCI chip at an address < 0x1000 on bus number 0
	c := lmsensors.Chip{
		Prefix: "amdgpu",
		Addr:   0x0300,
		Bus: lmsensors.Bus{
			Type: BusTypePci,
			Nr:   0,
		},
		Path: "/sys/class/hwmon/hwmon2",
	}

	// WHEN
	result := computeIdentifier(c)

	// THEN it is identical to the libsensors name ("%s-pci-%04x")
	assert.Equal(t, "amdgpu-pci-0300", result)
}

func TestComputeLmSensorsName(t *testing.T) {
	tests := []struct {
		name     string
		chip     lmsensors.Chip
		expected string
	}{
		{name: "pci, address >= 0x1000", chip: lmsensors.Chip{Prefix: "nvme", Addr: 0x5a00, Bus: lmsensors.Bus{Type: BusTypePci}}, expected: "nvme-pci-5a00"},
		{name: "pci, address < 0x1000", chip: lmsensors.Chip{Prefix: "amdgpu", Addr: 0x0300, Bus: lmsensors.Bus{Type: BusTypePci}}, expected: "amdgpu-pci-0300"},
		{name: "isa", chip: lmsensors.Chip{Prefix: "nct6798", Addr: 0x290, Bus: lmsensors.Bus{Type: BusTypeIsa}}, expected: "nct6798-isa-0290"},
		// fan2go names this chip "ucsi_source_psy_USBC000:002-isa-10f1", see TestComputeIdentifierIsa
		{name: "isa, bus number 1", chip: lmsensors.Chip{Prefix: "ucsi_source_psy_USBC000:002", Addr: 0x0f1, Bus: lmsensors.Bus{Type: BusTypeIsa, Nr: 1}}, expected: "ucsi_source_psy_USBC000:002-isa-00f1"},
		{name: "i2c", chip: lmsensors.Chip{Prefix: "jc42", Addr: 0x1b, Bus: lmsensors.Bus{Type: BusTypeI2c, Nr: 0}}, expected: "jc42-i2c-0-1b"},
		{name: "i2c, short address", chip: lmsensors.Chip{Prefix: "jc42", Addr: 0x8, Bus: lmsensors.Bus{Type: BusTypeI2c, Nr: 3}}, expected: "jc42-i2c-3-08"},
		{name: "spi", chip: lmsensors.Chip{Prefix: "chip", Addr: 0x1, Bus: lmsensors.Bus{Type: BusTypeSpi, Nr: 2}}, expected: "chip-spi-2-1"},
		{name: "virtual", chip: lmsensors.Chip{Prefix: "iwlwifi_1", Addr: 0, Bus: lmsensors.Bus{Type: BusTypeVirtual}}, expected: "iwlwifi_1-virtual-0"},
		{name: "acpi", chip: lmsensors.Chip{Prefix: "acpitz", Addr: 0, Bus: lmsensors.Bus{Type: BusTypeAcpi}}, expected: "acpitz-acpi-0"},
		{name: "hid", chip: lmsensors.Chip{Prefix: "corsaircpro", Addr: 0x3, Bus: lmsensors.Bus{Type: BusTypeHid, Nr: 3}}, expected: "corsaircpro-hid-3-3"},
		{name: "mdio", chip: lmsensors.Chip{Prefix: "r8169_0_600:00", Addr: 0, Bus: lmsensors.Bus{Type: BusTypeMdio}}, expected: "r8169_0_600:00-mdio-0"},
		{name: "scsi", chip: lmsensors.Chip{Prefix: "drivetemp", Addr: 0, Bus: lmsensors.Bus{Type: BusTypeScsi, Nr: 6}}, expected: "drivetemp-scsi-6-0"},
		{name: "unknown bus type", chip: lmsensors.Chip{Prefix: "chip", Bus: lmsensors.Bus{Type: 42}}, expected: ""},
		{name: "no prefix", chip: lmsensors.Chip{Addr: 0x5a00, Bus: lmsensors.Bus{Type: BusTypePci}}, expected: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, computeLmSensorsName(tt.chip))
		})
	}
}

// highAddressNvmeControllers returns two controllers: one whose fan2go and libsensors names differ
// (PCI address 0x5a00), and one where they are identical
func highAddressNvmeControllers() []*HwMonController {
	return []*HwMonController{
		{
			Platform:      "nvme-pci-0400",
			LmSensorsName: "nvme-pci-0400",
			Fans: []*fans.HwMonFan{
				{Config: configuration.FanConfig{HwMon: &configuration.HwMonFanConfig{
					Index: 1, RpmChannel: 1, PwmChannel: 1, SysfsPath: "/sys/hwmon1",
				}}},
			},
			Sensors: map[int]*sensors.HwmonSensor{
				1: {Index: 1, Channel: 1, Input: "/sys/hwmon1/temp1_input"},
			},
		},
		{
			Platform:      "nvme-pci-05a00",
			LmSensorsName: "nvme-pci-5a00",
			Fans: []*fans.HwMonFan{
				{Config: configuration.FanConfig{HwMon: &configuration.HwMonFanConfig{
					Index: 1, RpmChannel: 1, PwmChannel: 1, SysfsPath: "/sys/hwmon3",
				}}},
			},
			Sensors: map[int]*sensors.HwmonSensor{
				1: {Index: 1, Channel: 1, Input: "/sys/hwmon3/temp1_input"},
			},
		},
	}
}

func TestUpdateConfigFromHwMonControllers_PlatformNames(t *testing.T) {
	var tests = []struct {
		tn       string
		platform string
		wantPath string
	}{
		{tn: "fan2go name", platform: "nvme-pci-05a00", wantPath: "/sys/hwmon3"},
		{tn: "libsensors name", platform: "nvme-pci-5a00", wantPath: "/sys/hwmon3"},
		{tn: "libsensors name, different case", platform: "NVME-PCI-5A00", wantPath: "/sys/hwmon3"},
		{tn: "identical names", platform: "nvme-pci-0400", wantPath: "/sys/hwmon1"},
		// "nvme" matches the fan2go names of both controllers, the first one wins, like before
		{tn: "regex matching several controllers", platform: "nvme", wantPath: "/sys/hwmon1"},
	}

	for _, tt := range tests {
		t.Run("fan, "+tt.tn, func(t *testing.T) {
			config := configuration.FanConfig{
				ID:    "fan",
				HwMon: &configuration.HwMonFanConfig{Platform: tt.platform, Index: 1},
			}

			err := UpdateFanConfigFromHwMonControllers(highAddressNvmeControllers(), &config)

			assert.NoError(t, err)
			assert.Equal(t, tt.wantPath, config.HwMon.SysfsPath)
			assert.Equal(t, tt.wantPath+"/pwm1", config.HwMon.PwmPath)
			// the configured platform is kept as it is
			assert.Equal(t, tt.platform, config.HwMon.Platform)
		})
		t.Run("sensor, "+tt.tn, func(t *testing.T) {
			config := configuration.SensorConfig{
				ID:    "sensor",
				HwMon: &configuration.HwMonSensorConfig{Platform: tt.platform, Index: 1},
			}

			err := UpdateSensorConfigFromHwMonControllers(highAddressNvmeControllers(), &config)

			assert.NoError(t, err)
			assert.Equal(t, tt.wantPath+"/temp1_input", config.HwMon.TempInput)
		})
	}
}

func TestUpdateFanConfigFromHwMonControllers_Fan2goNameTakesPrecedence(t *testing.T) {
	// GIVEN a platform regex that matches the libsensors name of the first controller
	// and the fan2go name of the second one
	controllers := []*HwMonController{
		{
			Platform:      "chip-isa-00290",
			LmSensorsName: "chip-isa-0290-x",
			Fans: []*fans.HwMonFan{
				{Config: configuration.FanConfig{HwMon: &configuration.HwMonFanConfig{Index: 1, SysfsPath: "/sys/hwmon1"}}},
			},
		},
		{
			Platform:      "chip-isa-0290-x",
			LmSensorsName: "other",
			Fans: []*fans.HwMonFan{
				{Config: configuration.FanConfig{HwMon: &configuration.HwMonFanConfig{Index: 1, SysfsPath: "/sys/hwmon2"}}},
			},
		},
	}
	config := configuration.FanConfig{
		ID:    "fan",
		HwMon: &configuration.HwMonFanConfig{Platform: "chip-isa-0290-x", Index: 1},
	}

	// WHEN
	err := UpdateFanConfigFromHwMonControllers(controllers, &config)

	// THEN the fan2go name match wins, so configs that matched before keep resolving to the same fan
	assert.NoError(t, err)
	assert.Equal(t, "/sys/hwmon2", config.HwMon.SysfsPath)
}

func TestUpdateConfigFromHwMonControllers_InvalidPlatformRegex(t *testing.T) {
	fanConfig := configuration.FanConfig{
		ID:    "fan",
		HwMon: &configuration.HwMonFanConfig{Platform: "nvme-(", Index: 1},
	}
	err := UpdateFanConfigFromHwMonControllers(highAddressNvmeControllers(), &fanConfig)
	assert.ErrorContains(t, err, "failed to match platform regex of fan (nvme-()")

	sensorConfig := configuration.SensorConfig{
		ID:    "sensor",
		HwMon: &configuration.HwMonSensorConfig{Platform: "nvme-(", Index: 1},
	}
	err = UpdateSensorConfigFromHwMonControllers(highAddressNvmeControllers(), &sensorConfig)
	assert.ErrorContains(t, err, "failed to match platform regex of sensor (nvme-()")
}

func TestUpdateFanConfigFromHwMonControllers(t *testing.T) {
	var tests = []struct {
		tn            string
		hwMonConfigs  []configuration.HwMonFanConfig
		hwMonPlatform string
		configConfig  configuration.HwMonFanConfig
		wantConfig    *configuration.HwMonFanConfig
		wantErr       string
	}{{
		tn: "index config",
		hwMonConfigs: []configuration.HwMonFanConfig{
			{
				Index:      1,
				RpmChannel: 2,
				PwmChannel: 2,
				SysfsPath:  "/sys/hwmon1",
			},
		},
		configConfig: configuration.HwMonFanConfig{
			Index: 1,
		},
		wantConfig: &configuration.HwMonFanConfig{
			Index:         1,
			RpmChannel:    2,
			PwmChannel:    2,
			SysfsPath:     "/sys/hwmon1",
			RpmInputPath:  "/sys/hwmon1/fan2_input",
			PwmPath:       "/sys/hwmon1/pwm2",
			PwmEnablePath: "/sys/hwmon1/pwm2_enable",
		},
	}, {
		tn: "channel config",
		hwMonConfigs: []configuration.HwMonFanConfig{
			{
				Index:      1,
				RpmChannel: 2,
				PwmChannel: 2,
				SysfsPath:  "/sys/hwmon1",
			},
		},
		configConfig: configuration.HwMonFanConfig{
			RpmChannel: 2,
		},
		wantConfig: &configuration.HwMonFanConfig{
			Index:         1,
			RpmChannel:    2,
			PwmChannel:    2,
			SysfsPath:     "/sys/hwmon1",
			RpmInputPath:  "/sys/hwmon1/fan2_input",
			PwmPath:       "/sys/hwmon1/pwm2",
			PwmEnablePath: "/sys/hwmon1/pwm2_enable",
		},
	}, {
		tn: "pwm channel config",
		hwMonConfigs: []configuration.HwMonFanConfig{
			{
				Index:      1,
				RpmChannel: 2,
				PwmChannel: 2,
				SysfsPath:  "/sys/hwmon1",
			},
		},
		configConfig: configuration.HwMonFanConfig{
			RpmChannel: 2,
			PwmChannel: 3,
		},
		wantConfig: &configuration.HwMonFanConfig{
			Index:         1,
			RpmChannel:    2,
			PwmChannel:    3,
			SysfsPath:     "/sys/hwmon1",
			RpmInputPath:  "/sys/hwmon1/fan2_input",
			PwmPath:       "/sys/hwmon1/pwm3",
			PwmEnablePath: "/sys/hwmon1/pwm3_enable",
		},
	}, {
		tn: "no hwmon fans",
		configConfig: configuration.HwMonFanConfig{
			Index: 1,
		},
		wantErr: "no hwmon fan matched fan config",
	}, {
		tn: "no matching index",
		hwMonConfigs: []configuration.HwMonFanConfig{
			{
				Index: 2,
			},
		},
		configConfig: configuration.HwMonFanConfig{
			Index: 1,
		},
		wantErr: "no hwmon fan matched fan config",
	}, {
		tn: "no matching platform",
		hwMonConfigs: []configuration.HwMonFanConfig{
			{
				Index: 1,
			},
		},
		hwMonPlatform: "abc",
		configConfig: configuration.HwMonFanConfig{
			Index: 1,
		},
		wantErr: "no hwmon fan matched fan config",
	}}

	for _, tt := range tests {
		t.Run(tt.tn, func(t *testing.T) {
			// GIVEN
			fanSlice := []*fans.HwMonFan{}
			for _, c := range tt.hwMonConfigs {
				fanSlice = append(fanSlice, &fans.HwMonFan{
					Config: configuration.FanConfig{
						HwMon: &c,
					},
				})
			}
			if tt.hwMonPlatform == "" {
				tt.hwMonPlatform = "platform"
			}
			controllers := []*HwMonController{
				{
					Platform: tt.hwMonPlatform,
					Fans:     fanSlice,
				},
			}
			if tt.configConfig.Platform == "" {
				tt.configConfig.Platform = "platform"
			}
			config := configuration.FanConfig{
				HwMon: &tt.configConfig,
			}

			// WHEN
			err := UpdateFanConfigFromHwMonControllers(controllers, &config)

			// THEN
			if tt.wantConfig != nil {
				if tt.wantConfig.Platform == "" {
					tt.wantConfig.Platform = "platform"
				}
				assert.Equal(t, tt.wantConfig, config.HwMon)
			}
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUpdateSensorConfigFromHwMonControllers(t *testing.T) {
	var tests = []struct {
		tn            string
		hwmonSensors  map[int]*sensors.HwmonSensor
		hwMonPlatform string
		configConfig  configuration.HwMonSensorConfig
		wantConfig    *configuration.HwMonSensorConfig
		wantErr       string
	}{{
		tn: "match by index",
		hwmonSensors: map[int]*sensors.HwmonSensor{
			3: {Index: 1, Channel: 3, Input: "/sys/hwmon1/temp3_input"},
		},
		configConfig: configuration.HwMonSensorConfig{
			Index: 1,
		},
		wantConfig: &configuration.HwMonSensorConfig{
			Index:     1,
			Channel:   3,
			TempInput: "/sys/hwmon1/temp3_input",
		},
	}, {
		tn: "match by channel",
		hwmonSensors: map[int]*sensors.HwmonSensor{
			3: {Index: 1, Channel: 3, Input: "/sys/hwmon1/temp3_input"},
		},
		configConfig: configuration.HwMonSensorConfig{
			Channel: 3,
		},
		wantConfig: &configuration.HwMonSensorConfig{
			Index:     1,
			Channel:   3,
			TempInput: "/sys/hwmon1/temp3_input",
		},
	}, {
		tn: "no hwmon sensors",
		configConfig: configuration.HwMonSensorConfig{
			Index: 1,
		},
		wantErr: "no hwmon sensor matched sensor config",
	}, {
		tn: "no matching index",
		hwmonSensors: map[int]*sensors.HwmonSensor{
			3: {Index: 2, Channel: 3, Input: "/sys/hwmon1/temp3_input"},
		},
		configConfig: configuration.HwMonSensorConfig{
			Index: 1,
		},
		wantErr: "no hwmon sensor matched sensor config",
	}, {
		tn: "no matching channel",
		hwmonSensors: map[int]*sensors.HwmonSensor{
			3: {Index: 1, Channel: 3, Input: "/sys/hwmon1/temp3_input"},
		},
		configConfig: configuration.HwMonSensorConfig{
			Channel: 7,
		},
		wantErr: "no hwmon sensor matched sensor config",
	}, {
		tn: "no matching platform",
		hwmonSensors: map[int]*sensors.HwmonSensor{
			3: {Index: 1, Channel: 3, Input: "/sys/hwmon1/temp3_input"},
		},
		hwMonPlatform: "abc",
		configConfig: configuration.HwMonSensorConfig{
			Index: 1,
		},
		wantErr: "no hwmon sensor matched sensor config",
	}}

	for _, tt := range tests {
		t.Run(tt.tn, func(t *testing.T) {
			// GIVEN
			if tt.hwMonPlatform == "" {
				tt.hwMonPlatform = "platform"
			}
			controllers := []*HwMonController{
				{
					Platform: tt.hwMonPlatform,
					Sensors:  tt.hwmonSensors,
				},
			}
			if tt.configConfig.Platform == "" {
				tt.configConfig.Platform = "platform"
			}
			config := configuration.SensorConfig{
				HwMon: &tt.configConfig,
			}

			// WHEN
			err := UpdateSensorConfigFromHwMonControllers(controllers, &config)

			// THEN
			if tt.wantConfig != nil {
				if tt.wantConfig.Platform == "" {
					tt.wantConfig.Platform = "platform"
				}
				assert.Equal(t, tt.wantConfig, config.HwMon)
			}
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
