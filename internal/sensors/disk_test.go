package sensors

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Pure function tests: selectTempInput ---

func TestSelectTempInput_PrefersTemp1Input(t *testing.T) {
	paths := []string{
		"/sys/class/block/sda/device/hwmon/hwmon1/temp2_input",
		"/sys/class/block/sda/device/hwmon/hwmon1/temp1_input",
		"/sys/class/block/sda/device/hwmon/hwmon1/temp3_input",
	}
	got := selectTempInput(paths)
	assert.Equal(t, "/sys/class/block/sda/device/hwmon/hwmon1/temp1_input", got)
}

func TestSelectTempInput_FallsBackToFirst(t *testing.T) {
	paths := []string{
		"/sys/class/block/sda/device/hwmon/hwmon1/temp2_input",
		"/sys/class/block/sda/device/hwmon/hwmon1/temp3_input",
	}
	got := selectTempInput(paths)
	assert.Equal(t, "/sys/class/block/sda/device/hwmon/hwmon1/temp2_input", got)
}

func TestSelectTempInput_SinglePath(t *testing.T) {
	paths := []string{"/sys/class/block/sda/device/hwmon/hwmon1/temp5_input"}
	got := selectTempInput(paths)
	assert.Equal(t, paths[0], got)
}

// --- Pure function tests: parseAtaSmartAttributes ---

// buildAtaData builds a minimal SMART READ DATA payload (512 bytes) with one
// attribute at position 0 of the table.
func buildAtaData(attrID byte, rawByte0 byte) []byte {
	data := make([]byte, 512)
	// Attribute table starts at offset 2 within the 512-byte payload.
	// Entry 0: offset 2, layout: id(1), flags(2), current(1), worst(1), raw[0..5](6), reserved(1)
	off := 2
	data[off] = attrID     // id
	data[off+5] = rawByte0 // raw[0] = temperature in °C
	return data
}

func TestParseAtaSmartAttributes_Attr194(t *testing.T) {
	data := buildAtaData(194, 42) // attr 194, 42 °C
	temp, err := parseAtaSmartAttributes(data, "/dev/sda")
	require.NoError(t, err)
	assert.Equal(t, float64(42000), temp)
}

func TestParseAtaSmartAttributes_Attr190(t *testing.T) {
	data := buildAtaData(190, 35) // attr 190 (airflow), 35 °C
	temp, err := parseAtaSmartAttributes(data, "/dev/sda")
	require.NoError(t, err)
	assert.Equal(t, float64(35000), temp)
}

func TestParseAtaSmartAttributes_NoTempAttr(t *testing.T) {
	data := make([]byte, 512) // all zeros → attr ID 0 → no match
	_, err := parseAtaSmartAttributes(data, "/dev/sda")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no temperature attribute")
}

func TestParseAtaSmartAttributes_TruncatedData(t *testing.T) {
	// Fewer than 2+12 bytes → loop exits on first iteration
	data := make([]byte, 10)
	_, err := parseAtaSmartAttributes(data, "/dev/sda")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no temperature attribute")
}

// --- DiskSensor metadata tests ---

func newDiskSensor(device string) *DiskSensor {
	return &DiskSensor{
		Config: configuration.SensorConfig{
			ID:   "disk_test",
			Disk: &configuration.DiskSensorConfig{Device: device},
		},
	}
}

func TestDiskSensor_GetId(t *testing.T) {
	s := newDiskSensor("/dev/sda")
	assert.Equal(t, "disk_test", s.GetId())
}

func TestDiskSensor_GetLabel(t *testing.T) {
	s := newDiskSensor("/dev/sda")
	assert.Equal(t, "Disk (/dev/sda)", s.GetLabel())
}

func TestDiskSensor_GetConfig(t *testing.T) {
	s := newDiskSensor("/dev/sda")
	cfg := s.GetConfig()
	assert.Equal(t, "disk_test", cfg.ID)
	assert.Equal(t, "/dev/sda", cfg.Disk.Device)
}

func TestDiskSensor_MovingAvg(t *testing.T) {
	s := newDiskSensor("/dev/sda")
	s.SetMovingAvg(37500)
	assert.Equal(t, float64(37500), s.GetMovingAvg())
}

// --- Sysfs reading tests (fake tmpdir sysfs) ---

func writeTempFile(t *testing.T, dir, rel string, value string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(value), 0o644))
}

func TestReadDiskTempFromSysfsAt_SATA(t *testing.T) {
	tmp := t.TempDir()
	writeTempFile(t, tmp, "class/block/sda/device/hwmon/hwmon1/temp1_input", "38000\n")

	temp, err := readDiskTempFromSysfsAt(tmp, "sda")
	require.NoError(t, err)
	assert.Equal(t, float64(38000), temp)
}

func TestReadDiskTempFromSysfsAt_NVMe(t *testing.T) {
	tmp := t.TempDir()
	writeTempFile(t, tmp, "class/nvme/nvme0/hwmon0/temp1_input", "45000\n")

	temp, err := readDiskTempFromSysfsAt(tmp, "nvme0n1")
	require.NoError(t, err)
	assert.Equal(t, float64(45000), temp)
}

func TestReadDiskTempFromSysfsAt_PrefersTemp1(t *testing.T) {
	tmp := t.TempDir()
	writeTempFile(t, tmp, "class/block/sda/device/hwmon/hwmon1/temp2_input", "50000\n")
	writeTempFile(t, tmp, "class/block/sda/device/hwmon/hwmon1/temp1_input", "38000\n")

	temp, err := readDiskTempFromSysfsAt(tmp, "sda")
	require.NoError(t, err)
	assert.Equal(t, float64(38000), temp)
}

func TestReadDiskTempFromSysfsAt_NotFound(t *testing.T) {
	tmp := t.TempDir()
	_, err := readDiskTempFromSysfsAt(tmp, "sda")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no sysfs hwmon temperature")
}

// --- resolveDeviceAt tests ---

func makeSymlink(t *testing.T, dir, rel, target string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.Symlink(target, path))
}

func TestResolveDeviceAt_AbsolutePath(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "sda")
	require.NoError(t, os.WriteFile(real, nil, 0o644))

	got, err := resolveDeviceAt(real, tmp+"/dev", tmp+"/dev/disk/by-id")
	require.NoError(t, err)
	assert.Equal(t, real, got)
}

func TestResolveDeviceAt_AbsolutePath_Missing(t *testing.T) {
	tmp := t.TempDir()
	_, err := resolveDeviceAt(tmp+"/nonexistent", tmp+"/dev", tmp+"/dev/disk/by-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve device")
}

func TestResolveDeviceAt_RelativeViaDevBase(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "sda")
	require.NoError(t, os.WriteFile(real, nil, 0o644))
	makeSymlink(t, tmp, "dev/sda", real)

	got, err := resolveDeviceAt("sda", tmp+"/dev", tmp+"/dev/disk/by-id")
	require.NoError(t, err)
	assert.Equal(t, real, got)
}

func TestResolveDeviceAt_RelativeViaByIdBase(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "sda")
	require.NoError(t, os.WriteFile(real, nil, 0o644))
	// "ata-WD…" does NOT exist under devBase, but does under byIdBase
	makeSymlink(t, tmp, "dev/disk/by-id/ata-WD_XXXX", real)

	got, err := resolveDeviceAt("ata-WD_XXXX", tmp+"/dev", tmp+"/dev/disk/by-id")
	require.NoError(t, err)
	assert.Equal(t, real, got)
}

func TestResolveDeviceAt_RelativeNotFound(t *testing.T) {
	tmp := t.TempDir()
	_, err := resolveDeviceAt("nonexistent", tmp+"/dev", tmp+"/dev/disk/by-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve device")
}

// --- GetValue error-path test ---

func TestDiskSensor_GetValue_DeviceNotFound(t *testing.T) {
	s := newDiskSensor("/dev/nonexistent_abc_xyz")
	_, err := s.GetValue()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve")
}
