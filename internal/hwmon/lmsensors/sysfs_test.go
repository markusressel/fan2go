package lmsensors

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSysfs builds a sysfs tree below a temporary directory
type fakeSysfs struct {
	t    *testing.T
	root string
}

func newFakeSysfs(t *testing.T) *fakeSysfs {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "class", "hwmon"), 0o755))
	return &fakeSysfs{t: t, root: root}
}

func (f *fakeSysfs) path(rel string) string {
	return filepath.Join(f.root, rel)
}

func (f *fakeSysfs) file(rel string, content string, mode os.FileMode) {
	p := f.path(rel)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(f.t, os.WriteFile(p, []byte(content), mode))
}

func (f *fakeSysfs) symlink(target string, rel string) {
	p := f.path(rel)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(f.t, os.Symlink(f.path(target), p))
}

// device creates a device directory with its subsystem link
func (f *fakeSysfs) device(devRel string, subsystem string) {
	require.NoError(f.t, os.MkdirAll(f.path(devRel), 0o755))
	if subsystem != "" {
		require.NoError(f.t, os.MkdirAll(f.path("bus/"+subsystem), 0o755))
		f.symlink("bus/"+subsystem, devRel+"/subsystem")
	}
}

// hwmon creates /class/hwmon/<name> for the device, attributes are created relative to the returned directory
func (f *fakeSysfs) hwmon(name string, devRel string) string {
	hwmonRel := devRel + "/hwmon/" + name
	if devRel == "" {
		hwmonRel = "devices/virtual/hwmon/" + name
	}
	require.NoError(f.t, os.MkdirAll(f.path(hwmonRel), 0o755))
	f.symlink(hwmonRel, "class/hwmon/"+name)
	if devRel != "" {
		f.symlink(devRel, hwmonRel+"/device")
	}
	return hwmonRel
}

func wantInputs(t SubFeatureType) bool {
	return t == SubFeatureTypeTempInput || t == SubFeatureTypeTempMax || t == SubFeatureTypeFanInput
}

func chipsByPrefix(chips []Chip) map[string]Chip {
	result := map[string]Chip{}
	for _, c := range chips {
		result[c.Prefix] = c
	}
	return result
}

func TestSysfsChips_BusTypesAndAddresses(t *testing.T) {
	f := newFakeSysfs(t)

	// i2c
	f.device("devices/pci0000:00/i2c-0/0-001b", "i2c")
	h := f.hwmon("hwmon1", "devices/pci0000:00/i2c-0/0-001b")
	f.file(h+"/name", "jc42\n", 0o444)
	f.file(h+"/temp1_input", "30500\n", 0o444)
	// i2c bus of an ISA adapter counts as ISA
	f.device("devices/platform/i2c-5/5-0290", "i2c")
	f.file("devices/platform/i2c-5/name", "ISA main adapter\n", 0o444)
	f.symlink("devices/platform/i2c-5", "class/i2c-adapter/i2c-5/device")
	h = f.hwmon("hwmon2", "devices/platform/i2c-5/5-0290")
	f.file(h+"/name", "w83627hf\n", 0o444)
	f.file(h+"/temp1_input", "30500\n", 0o444)
	// pci
	f.device("devices/pci0000:00/0000:5a:00.0", "pci")
	h = f.hwmon("hwmon3", "devices/pci0000:00/0000:5a:00.0")
	f.file(h+"/name", "nvme\n", 0o444)
	f.file(h+"/temp1_input", "40000\n", 0o444)
	// platform with address
	f.device("devices/platform/nct6775.656", "platform")
	h = f.hwmon("hwmon4", "devices/platform/nct6775.656")
	f.file(h+"/name", "nct6798\n", 0o444)
	f.file(h+"/fan1_input", "800\n", 0o444)
	// platform with a name libsensors 3.6.2 reads a hex address from
	f.device("devices/platform/asus-ec-sensors", "platform")
	h = f.hwmon("hwmon5", "devices/platform/asus-ec-sensors")
	f.file(h+"/name", "asusec\n", 0o444)
	f.file(h+"/temp1_input", "40000\n", 0o444)
	// virtual (no device link)
	h = f.hwmon("hwmon6", "")
	f.file(h+"/name", "acpitz\n", 0o444)
	f.file(h+"/temp1_input", "27800\n", 0o444)
	// scsi
	f.device("devices/pci0000:00/ata7/host6/target6:0:0/6:0:0:0", "scsi")
	h = f.hwmon("hwmon7", "devices/pci0000:00/ata7/host6/target6:0:0/6:0:0:0")
	f.file(h+"/name", "drivetemp\n", 0o444)
	f.file(h+"/temp1_input", "35000\n", 0o444)
	// hid
	f.device("devices/usb1/0003:1B1C:0C10.0003", "hid")
	h = f.hwmon("hwmon8", "devices/usb1/0003:1B1C:0C10.0003")
	f.file(h+"/name", "corsaircpro\n", 0o444)
	f.file(h+"/fan1_input", "900\n", 0o444)
	// unknown subsystem without a parent device: virtual
	f.device("devices/something/thing0", "weird")
	h = f.hwmon("hwmon9", "devices/something/thing0")
	f.file(h+"/name", "weird\n", 0o444)
	f.file(h+"/temp1_input", "1000\n", 0o444)
	// no name: ignored
	f.device("devices/pci0000:00/0000:01:00.0", "pci")
	h = f.hwmon("hwmon10", "devices/pci0000:00/0000:01:00.0")
	f.file(h+"/temp1_input", "1000\n", 0o444)
	// no known attributes: ignored
	f.device("devices/pci0000:00/0000:02:00.0", "pci")
	h = f.hwmon("hwmon11", "devices/pci0000:00/0000:02:00.0")
	f.file(h+"/name", "empty\n", 0o444)
	f.file(h+"/power_state", "D0\n", 0o444)

	chips, err := SysfsChips(f.root, wantInputs)
	require.NoError(t, err)
	byPrefix := chipsByPrefix(chips)
	assert.Len(t, chips, 9)

	expected := map[string]struct {
		bus  Bus
		addr int32
	}{
		"jc42":        {Bus{Type: BusTypeI2c, Nr: 0}, 0x1b},
		"w83627hf":    {Bus{Type: BusTypeIsa, Nr: 0}, 0x290},
		"nvme":        {Bus{Type: BusTypePci, Nr: 0}, 0x5a00},
		"nct6798":     {Bus{Type: BusTypeIsa, Nr: 0}, 656},
		"asusec":      {Bus{Type: BusTypeIsa, Nr: 0}, 0xa},
		"acpitz":      {Bus{Type: BusTypeVirtual, Nr: 0}, 0},
		"drivetemp":   {Bus{Type: BusTypeScsi, Nr: 6}, 0},
		"corsaircpro": {Bus{Type: BusTypeHid, Nr: 3}, 3},
		"weird":       {Bus{Type: BusTypeVirtual, Nr: 0}, 0},
	}
	for prefix, e := range expected {
		chip, ok := byPrefix[prefix]
		if !assert.True(t, ok, prefix) {
			continue
		}
		assert.Equal(t, e.bus, chip.Bus, prefix)
		assert.Equal(t, e.addr, chip.Addr, prefix)
		assert.Equal(t, filepath.Join(f.root, "class", "hwmon"), filepath.Dir(chip.Path), prefix)
	}
}

func TestSysfsChips_AttributesOfTheDevice(t *testing.T) {
	f := newFakeSysfs(t)
	// old drivers: the hwmon class device has no attributes, they are in the device directory
	f.device("devices/platform/it87.552", "platform")
	h := f.hwmon("hwmon0", "devices/platform/it87.552")
	_ = h
	f.file("devices/platform/it87.552/name", "it8728\n", 0o444)
	f.file("devices/platform/it87.552/temp1_input", "42000\n", 0o444)

	chips, err := SysfsChips(f.root, wantInputs)

	require.NoError(t, err)
	require.Len(t, chips, 1)
	assert.Equal(t, "it8728", chips[0].Prefix)
	assert.Equal(t, Bus{Type: BusTypeIsa}, chips[0].Bus)
	assert.Equal(t, int32(552), chips[0].Addr)
	assert.Equal(t, f.path("devices/platform/it87.552"), chips[0].Path)
}

func TestSysfsChips_FeaturesSortedAndScaled(t *testing.T) {
	f := newFakeSysfs(t)
	f.device("devices/platform/nct6775.656", "platform")
	h := f.hwmon("hwmon0", "devices/platform/nct6775.656")
	f.file(h+"/name", "nct6798\n", 0o444)
	f.file(h+"/temp10_input", "50000\n", 0o444)
	f.file(h+"/temp2_input", "41500\n", 0o444)
	f.file(h+"/temp2_max", "80000\n", 0o644)
	f.file(h+"/temp2_alarm", "0\n", 0o444)
	f.file(h+"/temp2_label", "SYSTIN\n", 0o444)
	f.file(h+"/fan1_input", "1234\n", 0o444)
	f.file(h+"/fan1_min", "0\n", 0o644)
	f.file(h+"/pwm1", "128\n", 0o644)
	f.file(h+"/pwm1_enable", "2\n", 0o644)
	f.file(h+"/pwm1_auto_point1_pwm", "0\n", 0o644)
	f.file(h+"/in0_input", "1000\n", 0o444)
	f.file(h+"/uevent", "\n", 0o644)
	// unreadable input: value isn't read
	f.file(h+"/temp3_input", "60000\n", 0o200)

	chips, err := SysfsChips(f.root, wantInputs)
	require.NoError(t, err)
	require.Len(t, chips, 1)

	type sub struct {
		Name  string
		Type  SubFeatureType
		Flags uint32
		Value float64
	}
	var names []string
	subs := map[string][]sub{}
	for _, feature := range chips[0].Features {
		names = append(names, feature.Name)
		for _, s := range feature.SubFeatures {
			subs[feature.Name] = append(subs[feature.Name], sub{s.Name, s.Type, s.Flags, s.Value})
		}
	}
	// by feature type (in, fan, temp, pwm), then channel
	assert.Equal(t, []string{"in0", "fan1", "temp2", "temp3", "temp10", "pwm1"}, names)
	assert.Equal(t, []sub{
		{"temp2_input", SubFeatureTypeTempInput, 1 | 4, 41.5},
		{"temp2_max", SubFeatureTypeTempMax, 1 | 2 | 4, 80},
		{"temp2_alarm", 0x280, 1, 0},
	}, subs["temp2"])
	assert.Equal(t, []sub{{"temp3_input", SubFeatureTypeTempInput, 2 | 4, 0}}, subs["temp3"])
	assert.Equal(t, []sub{{"temp10_input", SubFeatureTypeTempInput, 1 | 4, 50}}, subs["temp10"])
	assert.Equal(t, []sub{
		{"fan1_input", SubFeatureTypeFanInput, 1 | 4, 1234},
		{"fan1_min", SubFeatureTypeFanMin, 1 | 2 | 4, 0},
	}, subs["fan1"])
	assert.Equal(t, []sub{
		{"pwm1", 0x700, 1 | 2 | 4, 0},
		{"pwm1_enable", SubFeatureTypePwmEnable, 1 | 2, 0},
	}, subs["pwm1"])

	// numbers are consecutive over the chip, like in libsensors
	var number int32
	for i, feature := range chips[0].Features {
		assert.Equal(t, int32(i), feature.Number)
		for _, s := range feature.SubFeatures {
			assert.Equal(t, number, s.Number)
			number++
		}
	}
}

func TestSysfsChips_NoHwmonClass(t *testing.T) {
	_, err := SysfsChips(t.TempDir(), nil)
	assert.Error(t, err)
}

func TestParseSubFeatureName(t *testing.T) {
	tests := []struct {
		name string
		t    SubFeatureType
		nr   int
		ok   bool
	}{
		{"temp1_input", SubFeatureTypeTempInput, 0, true},
		{"temp12_max", SubFeatureTypeTempMax, 11, true},
		{"in0_input", 0x000, 0, true},
		{"fan3_input", SubFeatureTypeFanInput, 2, true},
		{"pwm2", 0x700, 1, true},
		{"pwm2_enable", SubFeatureTypePwmEnable, 1, true},
		{"intrusion0_alarm", 0x1100, 0, true},
		{"cpu0_vid", 0x1000, 0, true},
		{"beep_enable", 0x1800, 0, true},
		{"temp0_input", 0, 0, false},
		{"temp1_label", 0, 0, false},
		{"pwm1_auto_point1_pwm", 0, 0, false},
		{"name", 0, 0, false},
		{"temp1", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ, nr, ok := parseSubFeatureName(tt.name)
			assert.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.Equal(t, tt.t, typ)
				assert.Equal(t, tt.nr, nr)
			}
		})
	}
}
