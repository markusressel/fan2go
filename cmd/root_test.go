package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validRootTestConfig is a config that passes validation
const validRootTestConfig = `
fans:
  - id: fan
    file:
      path: /tmp/fan2go-test-fan
    curve: curve
curves:
  - id: curve
    linear:
      sensor: sensor
      min: 40
      max: 80
sensors:
  - id: sensor
    file:
      path: /tmp/fan2go-test-sensor
`

// runRootCommand runs `fan2go -c <config>` (the daemon command) with the given config content,
// without the os.Exit of Execute. The daemon itself doesn't run, see stubRunDaemon.
func runRootCommand(t *testing.T, content string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fan2go.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return runRootCommandWithConfigPath(t, path, nil)
}

// runRootCommandWithConfigPath runs `fan2go -c <path>`. If the config is valid, daemon runs instead of
// the real daemon; it must not be nil then.
func runRootCommandWithConfigPath(t *testing.T, path string, daemon func() error) error {
	t.Helper()
	previousRunDaemon := runDaemon
	runDaemon = func() error {
		if daemon == nil {
			t.Error("the daemon must not run in this test")
			return nil
		}
		return daemon()
	}
	t.Cleanup(func() { runDaemon = previousRunDaemon })
	viper.Reset()
	t.Cleanup(viper.Reset)
	previousConfig := configuration.CurrentConfig
	t.Cleanup(func() { configuration.CurrentConfig = previousConfig })
	configuration.InitConfig(path)

	// don't send desktop notifications from tests
	previousErrorAndNotify := errorAndNotify
	errorAndNotify = func(title string, format string, a ...interface{}) {
		t.Logf("%s: "+format, append([]interface{}{title}, a...)...)
	}
	t.Cleanup(func() { errorAndNotify = previousErrorAndNotify })

	rootCmd.SetArgs([]string{})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	return rootCmd.Execute()
}

func TestRootCommand_ConfigValidationErrorFails(t *testing.T) {
	// GIVEN a config whose fan references a curve that doesn't exist
	err := runRootCommand(t, `
fans:
  - id: fan
    file:
      path: /tmp/fan2go-test-fan
    curve: missing
`)

	// THEN the command fails with the config error exit code instead of returning normally
	assert.ErrorContains(t, err, "no curve definition with id 'missing' found")
	var logged loggedError
	assert.ErrorAs(t, err, &logged, "the error has been logged already, Execute must not print it again")
	assert.Equal(t, ExitCodeConfigError, exitCode(err))
}

func TestRootCommand_MissingConfigFileFails(t *testing.T) {
	// GIVEN a config path that doesn't exist
	path := filepath.Join(t.TempDir(), "missing.yaml")

	// WHEN
	err := runRootCommandWithConfigPath(t, path, nil)

	// THEN
	assert.Error(t, err)
	var logged loggedError
	assert.ErrorAs(t, err, &logged)
	assert.Equal(t, ExitCodeConfigError, exitCode(err))
}

func TestRootCommand_DaemonErrors(t *testing.T) {
	tests := []struct {
		name         string
		daemonErr    error
		expectedCode int
	}{
		{name: "clean exit", daemonErr: nil, expectedCode: ExitCodeOk},
		{name: "runtime error", daemonErr: errors.New("no valid fan configurations, exiting"), expectedCode: ExitCodeError},
		{name: "config error", daemonErr: configuration.NewConfigError(errors.New("duplicate curve id")), expectedCode: ExitCodeConfigError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN a valid config, and a daemon that stops with the given error
			path := filepath.Join(t.TempDir(), "fan2go.yaml")
			require.NoError(t, os.WriteFile(path, []byte(validRootTestConfig), 0o600))

			// WHEN
			daemonRan := false
			err := runRootCommandWithConfigPath(t, path, func() error {
				daemonRan = true
				return tt.daemonErr
			})

			// THEN
			assert.True(t, daemonRan)
			assert.Equal(t, tt.expectedCode, exitCode(err))
			if tt.daemonErr != nil {
				var logged loggedError
				assert.ErrorAs(t, err, &logged, "the daemon error has been logged already")
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	configErr := configuration.NewConfigError(errors.New("invalid"))
	assert.Equal(t, ExitCodeOk, exitCode(nil))
	assert.Equal(t, ExitCodeError, exitCode(errors.New("some error")))
	assert.Equal(t, ExitCodeConfigError, exitCode(configErr))
	assert.Equal(t, ExitCodeConfigError, exitCode(fmt.Errorf("wrapped: %w", configErr)))
	assert.Equal(t, ExitCodeConfigError, exitCode(loggedError{configErr}))
	assert.Equal(t, 78, ExitCodeConfigError, "fan2go.service relies on this value (RestartPreventExitStatus)")
}

func TestRootCommand_ConfigLoadingErrorFails(t *testing.T) {
	// GIVEN a config that can't be decoded
	err := runRootCommand(t, `
fans: "not a list"
`)

	// THEN
	assert.Error(t, err)
	var logged loggedError
	assert.ErrorAs(t, err, &logged)
	assert.Equal(t, ExitCodeConfigError, exitCode(err))
}

func TestPrintUnloggedError(t *testing.T) {
	var out bytes.Buffer
	printUnloggedError(&out, errors.New("some error"))
	assert.Equal(t, "Error: some error\n", out.String())

	out.Reset()
	printUnloggedError(&out, fmt.Errorf("wrapped: %w", loggedError{errors.New("already logged")}))
	assert.Empty(t, out.String(), "errors that have been logged already are not printed again")
}

func TestPlatformText(t *testing.T) {
	tests := []struct {
		name       string
		controller hwmon.HwMonController
		expected   string
	}{
		{name: "identical names", controller: hwmon.HwMonController{Platform: "nvme-pci-0400", LmSensorsName: "nvme-pci-0400"}, expected: "nvme-pci-0400"},
		{name: "different names", controller: hwmon.HwMonController{Platform: "nvme-pci-05a00", LmSensorsName: "nvme-pci-5a00"}, expected: "nvme-pci-05a00 (sensors: nvme-pci-5a00)"},
		{name: "no libsensors name", controller: hwmon.HwMonController{Platform: "nvme-pci-05a00"}, expected: "nvme-pci-05a00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, platformText(&tt.controller))
		})
	}
}

func TestRootCommand_MissingRequiredFlagPrintsUsage(t *testing.T) {
	// GIVEN
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
	})

	// earlier commands in this process may have silenced the usage (see PersistentPreRunE)
	fanCurveCmd, _, findErr := rootCmd.Find([]string{"fan", "curve"})
	require.NoError(t, findErr)
	rootCmd.SilenceUsage = false
	fanCurveCmd.SilenceUsage = false

	// WHEN: a command with a required flag runs without it
	rootCmd.SetArgs([]string{"fan", "curve"})
	err := rootCmd.Execute()

	// THEN: it fails as a usage error (exit code 1), and the usage is printed
	assert.ErrorContains(t, err, `required flag(s) "id" not set`)
	assert.Equal(t, ExitCodeError, exitCode(err))
	assert.Contains(t, out.String(), "Usage:")
}

func TestConfigValidateCommand_InvalidConfigExitsWithConfigErrorCode(t *testing.T) {
	// GIVEN a config whose fan references a curve that doesn't exist
	path := filepath.Join(t.TempDir(), "fan2go.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
fans:
  - id: fan
    file:
      path: /tmp/fan2go-test-fan
    curve: missing
`), 0o600))
	viper.Reset()
	t.Cleanup(viper.Reset)
	previousConfig := configuration.CurrentConfig
	t.Cleanup(func() { configuration.CurrentConfig = previousConfig })
	require.NoError(t, configuration.InitConfig(path))
	rootCmd.SetArgs([]string{"config", "validate"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	// WHEN
	err := rootCmd.Execute()

	// THEN
	assert.ErrorContains(t, err, "no curve definition with id 'missing' found")
	assert.Equal(t, ExitCodeConfigError, exitCode(err))
}

func TestRootCommand_InitConfigErrorDoesNotBlockHelp(t *testing.T) {
	// GIVEN: InitConfig failed
	previous := initConfigErr
	initConfigErr = configuration.NewConfigError(errors.New("couldn't detect home directory"))
	t.Cleanup(func() { initConfigErr = previous })
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetArgs(nil)
	})

	for _, args := range [][]string{{"help", "fan"}, {"completion", "bash"}} {
		// WHEN
		rootCmd.SetArgs(args)
		err := rootCmd.Execute()

		// THEN
		assert.NoError(t, err, "%v", args)
	}
}

func TestRootCommand_InitConfigErrorFails(t *testing.T) {
	// GIVEN: InitConfig failed
	previous := initConfigErr
	initConfigErr = configuration.NewConfigError(errors.New("couldn't detect home directory"))
	t.Cleanup(func() { initConfigErr = previous })
	rootCmd.SetArgs([]string{"config", "validate"})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	// WHEN
	err := rootCmd.Execute()

	// THEN
	assert.ErrorContains(t, err, "couldn't detect home directory")
	assert.Equal(t, ExitCodeConfigError, exitCode(err))
}
