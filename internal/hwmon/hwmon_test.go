package hwmon

import (
	"fmt"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/md14454/gosensors"
	"github.com/stretchr/testify/assert"
)

func TestComputeIdentifierIsa(t *testing.T) {
	// GIVEN
	c := gosensors.Chip{
		Prefix: "ucsi_source_psy_USBC000:002",
		Addr:   0x0f1,
		Bus: gosensors.Bus{
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
	c := gosensors.Chip{
		Prefix: "nvme",
		Addr:   0x5,
		Bus: gosensors.Bus{
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
	c := gosensors.Chip{
		Prefix: "nvme",
		Bus: gosensors.Bus{
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

func TestFindPlatform(t *testing.T) {
	// GIVEN
	devicePath := "/sys/devices/pci0000:00/0000:00:0e.0/pci10000:e0/10000:e0:06.0/10000:e1:00.0/nvme/nvme0/hwmon3"

	// WHEN
	platform := findPlatform(devicePath)

	// THEN
	assert.Equal(t, "", platform)
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
			fanSlice := []fans.HwMonFan{}
			for _, c := range tt.hwMonConfigs {
				fanSlice = append(fanSlice, fans.HwMonFan{
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
