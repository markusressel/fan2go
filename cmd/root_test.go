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

// runRootCommand runs `fan2go -c <config>` (the daemon command) with the given config content,
// without the os.Exit of Execute
func runRootCommand(t *testing.T, content string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fan2go.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
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

	// THEN the command fails (so fan2go exits with a non-zero code) instead of returning normally
	assert.ErrorContains(t, err, "no curve definition with id 'missing' found")
	var logged loggedError
	assert.ErrorAs(t, err, &logged, "the error has been logged already, Execute must not print it again")
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
}

func TestPrintUnloggedError(t *testing.T) {
	var out bytes.Buffer
	printUnloggedError(&out, errors.New("some error"))
	assert.Equal(t, "some error\n", out.String())

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
