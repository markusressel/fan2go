package hwmon

import (
	"fmt"
	"github.com/md14454/gosensors"
	"github.com/stretchr/testify/assert"
	"testing"
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
