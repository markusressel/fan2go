package sensor

import (
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/stretchr/testify/assert"
)

func testControllers() []*hwmon.HwMonController {
	return []*hwmon.HwMonController{
		{
			Platform: "nvme-pci-0400",
			Sensors: map[int]*sensors.HwmonSensor{
				1: {Index: 1, Channel: 1, Input: "/sys/class/hwmon/hwmon2/temp1_input"},
			},
		},
		{
			Platform: "ice-pci-0100",
			Sensors: map[int]*sensors.HwmonSensor{
				1: {Index: 1, Channel: 1, Input: "/sys/class/hwmon/hwmon5/temp1_input"},
				2: {Index: 2, Channel: 3, Input: "/sys/class/hwmon/hwmon5/temp3_input"},
			},
		},
	}
}

func TestResolveHwmonSensorConfig_ByChannel(t *testing.T) {
	// GIVEN a sensor configured by channel only (issue #535)
	config := configuration.SensorConfig{
		ID:    "sfp28_temp",
		HwMon: &configuration.HwMonSensorConfig{Platform: "ice-pci-0100", Channel: 3},
	}

	// WHEN
	err := resolveHwmonSensorConfig(testControllers(), &config)

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, "/sys/class/hwmon/hwmon5/temp3_input", config.HwMon.TempInput)
	assert.Equal(t, 2, config.HwMon.Index)
}

func TestResolveHwmonSensorConfig_ByIndex(t *testing.T) {
	config := configuration.SensorConfig{
		ID:    "sfp28_temp",
		HwMon: &configuration.HwMonSensorConfig{Platform: "ice-pci-0100", Index: 2},
	}

	err := resolveHwmonSensorConfig(testControllers(), &config)

	assert.NoError(t, err)
	assert.Equal(t, "/sys/class/hwmon/hwmon5/temp3_input", config.HwMon.TempInput)
	assert.Equal(t, 3, config.HwMon.Channel)
}

func TestResolveHwmonSensorConfig_PlatformRegexSkipsControllersWithoutTheChannel(t *testing.T) {
	// GIVEN a generic platform regex that matches several controllers
	config := configuration.SensorConfig{
		ID:    "generic",
		HwMon: &configuration.HwMonSensorConfig{Platform: "pci", Channel: 3},
	}

	err := resolveHwmonSensorConfig(testControllers(), &config)

	assert.NoError(t, err)
	assert.Equal(t, "/sys/class/hwmon/hwmon5/temp3_input", config.HwMon.TempInput)
}

func TestResolveHwmonSensorConfig_NoMatchIsAnError(t *testing.T) {
	// previously this fell through with an empty input path: "open : no such file or directory"
	config := configuration.SensorConfig{
		ID:    "sfp28_temp",
		HwMon: &configuration.HwMonSensorConfig{Platform: "ice-pci-0100", Channel: 9},
	}

	err := resolveHwmonSensorConfig(testControllers(), &config)

	assert.ErrorContains(t, err, "unable to find hwmon sensor sfp28_temp")
}

func TestResolveHwmonSensorConfig_EmptyInputIsAnError(t *testing.T) {
	controllers := []*hwmon.HwMonController{{
		Platform: "ice-pci-0100",
		Sensors:  map[int]*sensors.HwmonSensor{1: {Index: 1, Channel: 1}},
	}}
	config := configuration.SensorConfig{
		ID:    "sfp28_temp",
		HwMon: &configuration.HwMonSensorConfig{Platform: "ice-pci-0100", Channel: 1},
	}

	err := resolveHwmonSensorConfig(controllers, &config)

	assert.ErrorContains(t, err, "unable to find temp input for sensor sfp28_temp")
}
