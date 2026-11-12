package fan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePwmValue(t *testing.T) {
	tests := []struct {
		arg     string
		want    int
		wantErr string
	}{
		{arg: "0", want: 0},
		{arg: "128", want: 128},
		{arg: "255", want: 255},
		{arg: "-1", wantErr: "invalid PWM value -1, must be in [0..255]"},
		{arg: "256", wantErr: "invalid PWM value 256, must be in [0..255]"},
		{arg: "1000", wantErr: "invalid PWM value 1000, must be in [0..255]"},
		{arg: "abc", wantErr: "invalid PWM value 'abc', must be an integer in [0..255]"},
		{arg: "", wantErr: "invalid PWM value '', must be an integer in [0..255]"},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			got, err := parsePwmValue(tt.arg)
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// useConfigFile makes getFan read the given config, like `fan2go -c <path> fan ...`
func useConfigFile(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fan2go.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	viper.Reset()
	t.Cleanup(viper.Reset)
	previousConfig := configuration.CurrentConfig
	t.Cleanup(func() { configuration.CurrentConfig = previousConfig })
	require.NoError(t, configuration.InitConfig(path))
}

func TestGetFan_HwMonFanNotFound(t *testing.T) {
	// GIVEN a hwmon fan whose platform doesn't exist
	useConfigFile(t, `
fans:
  - id: missing_fan
    hwmon:
      platform: fan2go-test-platform-that-does-not-exist
      rpmChannel: 1
    curve: curve
sensors:
  - id: sensor
    file:
      path: /tmp/fan2go-test-sensor
curves:
  - id: curve
    linear:
      sensor: sensor
      min: 40
      max: 80
`)

	// WHEN
	fan, err := getFan("missing_fan")

	// THEN the lookup error is returned instead of a fan without sysfs paths
	assert.Nil(t, fan)
	assert.ErrorContains(t, err, "fan missing_fan: couldn't find the hwmon fan: no hwmon fan matched fan config")
}

func TestGetFan_UnknownId(t *testing.T) {
	useConfigFile(t, `
fans:
  - id: file_fan
    file:
      path: /tmp/fan2go-test-fan
    curve: curve
sensors:
  - id: sensor
    file:
      path: /tmp/fan2go-test-sensor
curves:
  - id: curve
    linear:
      sensor: sensor
      min: 40
      max: 80
`)

	fan, err := getFan("other")

	assert.Nil(t, fan)
	assert.ErrorContains(t, err, "no fan with id found: other")
}
