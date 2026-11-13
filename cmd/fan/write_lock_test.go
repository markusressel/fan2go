package fan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useFileFanConfig configures a file fan "file_fan" at PWM 50 and a database in dbDir, and returns the PWM file.
func useFileFanConfig(t *testing.T, dbDir string) (pwmPath string) {
	t.Helper()
	dir := t.TempDir()
	pwmPath = filepath.Join(dir, "pwm")
	require.NoError(t, os.WriteFile(pwmPath, []byte("50"), 0o600))
	sensorPath := filepath.Join(dir, "sensor")
	require.NoError(t, os.WriteFile(sensorPath, []byte("40000"), 0o600))
	useConfigFile(t, fmt.Sprintf(`
dbPath: %s
fans:
  - id: file_fan
    file:
      path: %s
    curve: curve
sensors:
  - id: sensor
    file:
      path: %s
curves:
  - id: curve
    linear:
      sensor: sensor
      min: 40
      max: 80
`, filepath.Join(dbDir, "fan2go.db"), pwmPath, sensorPath))
	previousFanId := fanId
	fanId = "file_fan"
	t.Cleanup(func() { fanId = previousFanId })
	return pwmPath
}

func readPwm(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.TrimSpace(string(content))
}

// holdDaemonLock takes the process lock of the database like a running daemon.
func holdDaemonLock(t *testing.T, dbDir string) {
	t.Helper()
	release, err := persistence.AcquireProcessLock(filepath.Join(dbDir, "fan2go.db"))
	require.NoError(t, err)
	t.Cleanup(release)
}

func TestFanSpeed_DaemonRunning_RefusesToWrite(t *testing.T) {
	// GIVEN
	dbDir := t.TempDir()
	pwmPath := useFileFanConfig(t, dbDir)
	holdDaemonLock(t, dbDir)

	// WHEN
	err := speedCmd.RunE(speedCmd, []string{"100"})

	// THEN
	assert.ErrorContains(t, err, "can't set the speed of fan file_fan")
	assert.ErrorContains(t, err, "would overwrite the value")
	assert.ErrorContains(t, err, "Stop the daemon first")
	assert.Equal(t, "50", readPwm(t, pwmPath))
}

func TestFanSpeed_DaemonRunning_ReadingWorks(t *testing.T) {
	dbDir := t.TempDir()
	useFileFanConfig(t, dbDir)
	holdDaemonLock(t, dbDir)

	assert.NoError(t, speedCmd.RunE(speedCmd, []string{}))
}

func TestFanSpeed_NoDaemon_Writes(t *testing.T) {
	// GIVEN: no daemon, the database directory exists
	dbDir := t.TempDir()
	pwmPath := useFileFanConfig(t, dbDir)

	// WHEN
	err := speedCmd.RunE(speedCmd, []string{"100"})

	// THEN: written, and the lock is released again
	require.NoError(t, err)
	assert.Equal(t, "100", readPwm(t, pwmPath))
	holdDaemonLock(t, dbDir)
}

func TestFanSpeed_DatabaseDirectoryMissing_WritesWithoutCreatingIt(t *testing.T) {
	dbDir := filepath.Join(t.TempDir(), "missing")
	pwmPath := useFileFanConfig(t, dbDir)

	require.NoError(t, speedCmd.RunE(speedCmd, []string{"100"}))

	assert.Equal(t, "100", readPwm(t, pwmPath))
	assert.NoDirExists(t, dbDir)
}

func TestFanMode_DaemonRunning_RefusesToWriteButReads(t *testing.T) {
	dbDir := t.TempDir()
	useFileFanConfig(t, dbDir)
	holdDaemonLock(t, dbDir)

	err := modeCmd.RunE(modeCmd, []string{"auto"})

	assert.ErrorContains(t, err, "can't set the control mode of fan file_fan")
	assert.ErrorContains(t, err, "Stop the daemon first")
	assert.NoError(t, modeCmd.RunE(modeCmd, []string{}))
}

func TestFanRpmAndCurve_DaemonRunning_DontTakeTheLock(t *testing.T) {
	dbDir := t.TempDir()
	useFileFanConfig(t, dbDir)
	holdDaemonLock(t, dbDir)

	// a file fan has no RPM sensor, and the database holds no curve: both may fail, but not because of the lock
	for _, err := range []error{
		rpmCmd.RunE(rpmCmd, []string{}),
		curveCmd.RunE(curveCmd, []string{}),
	} {
		if err != nil {
			assert.NotContains(t, err.Error(), "Stop the daemon first")
		}
	}
}

// makeLockFileReadOnly creates the lock file like a daemon run as root would leave it: not writable for this user.
func makeLockFileReadOnly(t *testing.T, dbDir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root can write read-only files")
	}
	path := persistence.ProcessLockPath(filepath.Join(dbDir, "fan2go.db"))
	require.NoError(t, os.WriteFile(path, []byte("1\n"), 0o444))
}

func TestFanSpeed_LockFileNotWritable_NoDaemon_Writes(t *testing.T) {
	dbDir := t.TempDir()
	pwmPath := useFileFanConfig(t, dbDir)
	makeLockFileReadOnly(t, dbDir)

	require.NoError(t, speedCmd.RunE(speedCmd, []string{"100"}))

	assert.Equal(t, "100", readPwm(t, pwmPath))
}

func TestFanSpeed_LockFileNotWritable_DaemonRunning_RefusesToWrite(t *testing.T) {
	dbDir := t.TempDir()
	pwmPath := useFileFanConfig(t, dbDir)
	makeLockFileReadOnly(t, dbDir)
	// the daemon holds the lock through its own descriptor, as root could
	file, err := os.Open(persistence.ProcessLockPath(filepath.Join(dbDir, "fan2go.db")))
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	require.NoError(t, syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))

	err = speedCmd.RunE(speedCmd, []string{"100"})

	assert.ErrorContains(t, err, "Stop the daemon first")
	assert.Equal(t, "50", readPwm(t, pwmPath))
}
