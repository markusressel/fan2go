package internal

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestFindDeviceName(t *testing.T) {
	// GIVEN
	deviceName := "some-device-name"
	devicePathToExpectedName := map[string]string{
		"/sys/devices/platform/nct6775.656/hwmon/hwmon4":                                                 deviceName,
		"/sys/devices/pci0000:00/0000:00:0e.0/pci10000:e0/10000:e0:06.0/10000:e1:00.0/nvme/nvme0/hwmon3": fmt.Sprintf("%s-pci-10000E100", deviceName),
		"/sys/devices/pci0000:00/0000:00:01.2/0000:02:00.0/0000:03:01.0/0000:04:00.0/nvme/nvme1/hwmon0":  fmt.Sprintf("%s-pci-0400", deviceName),
	}

	for key, value := range devicePathToExpectedName {
		// WHEN
		result := computeIdentifier(key, deviceName)

		// THEN
		assert.Equal(t, value, result)
	}
}

func TestFindPlatform(t *testing.T) {
	// GIVEN
	devicePath := "/sys/devices/pci0000:00/0000:00:0e.0/pci10000:e0/10000:e0:06.0/10000:e1:00.0/nvme/nvme0/hwmon3"

	// WHEN
	platform := findPlatform(devicePath)

	// THEN
	assert.Equal(t, "", platform)
}
