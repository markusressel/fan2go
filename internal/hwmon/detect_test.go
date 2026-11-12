package hwmon

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/markusressel/fan2go/internal/hwmon/lmsensors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubDetection replaces the detection functions for the duration of the test and returns the logged warnings
func stubDetection(t *testing.T, libsensors func(lmsensors.WantValue) ([]lmsensors.Chip, error), root string) *[]string {
	t.Helper()
	oldLibsensors, oldRoot, oldWarnf := libsensorsChips, sysfsRoot, warnf
	var warnings []string
	libsensorsChips = libsensors
	sysfsRoot = root
	warnf = func(format string, a ...interface{}) { warnings = append(warnings, fmt.Sprintf(format, a...)) }
	libsensorsWarning, libsensorsInfo = sync.Once{}, sync.Once{}
	t.Cleanup(func() {
		libsensorsChips, sysfsRoot, warnf = oldLibsensors, oldRoot, oldWarnf
		libsensorsWarning, libsensorsInfo = sync.Once{}, sync.Once{}
	})
	return &warnings
}

// fakeSysfs creates <root>/class/hwmon/hwmon0 with a virtual chip "testchip" that has temp1_input
func fakeSysfs(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "devices", "virtual", "hwmon", "hwmon0")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "name"), []byte("testchip\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "temp1_input"), []byte("42000\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "class", "hwmon"), 0o755))
	require.NoError(t, os.Symlink(dir, filepath.Join(root, "class", "hwmon", "hwmon0")))
	return root
}

func TestDetectChips_UsesLibsensors(t *testing.T) {
	warnings := stubDetection(t, func(lmsensors.WantValue) ([]lmsensors.Chip, error) {
		return []lmsensors.Chip{{Prefix: "fromlibsensors"}}, nil
	}, t.TempDir())

	chips := detectChips()

	require.Len(t, chips, 1)
	assert.Equal(t, "fromlibsensors", chips[0].Prefix)
	assert.Empty(t, *warnings)
}

func TestDetectChips_FallsBackToSysfsAndWarnsOnce(t *testing.T) {
	loadErr := fmt.Errorf("%w (libsensors.so.5: cannot open shared object file)", lmsensors.ErrUnavailable)
	warnings := stubDetection(t, func(lmsensors.WantValue) ([]lmsensors.Chip, error) {
		return nil, loadErr
	}, fakeSysfs(t))

	for i := 0; i < 2; i++ {
		chips := detectChips()
		require.Len(t, chips, 1)
		assert.Equal(t, "testchip", chips[0].Prefix)
		require.Len(t, chips[0].Features, 1)
		assert.Equal(t, "temp1", chips[0].Features[0].Name)
	}

	require.Len(t, *warnings, 1)
	assert.Contains(t, (*warnings)[0], "Detecting hwmon chips directly in sysfs")
	assert.Contains(t, (*warnings)[0], "Install libsensors")
}

func TestDetectChips_SysfsFailure(t *testing.T) {
	stubDetection(t, func(lmsensors.WantValue) ([]lmsensors.Chip, error) {
		return nil, lmsensors.ErrUnavailable
	}, filepath.Join(t.TempDir(), "missing"))

	assert.Empty(t, detectChips())
}

func TestLibsensorsWarningText(t *testing.T) {
	notLoaded := libsensorsWarningText(fmt.Errorf("%w (x)", lmsensors.ErrUnavailable))
	assert.Contains(t, notLoaded, "Install libsensors")

	initFailed := libsensorsWarningText(fmt.Errorf("%w with %s: Parse error", lmsensors.ErrInit, lmsensors.ConfigFile))
	assert.Contains(t, initFailed, "Fix or remove "+lmsensors.ConfigFile)
	assert.NotContains(t, initFailed, "Install libsensors")

	noCgo := libsensorsWarningText(fmt.Errorf("%w: %w", lmsensors.ErrUnavailable, lmsensors.ErrNoCgo))
	assert.NotContains(t, noCgo, "Install libsensors")
	assert.NotContains(t, noCgo, "Fix or remove")
}
