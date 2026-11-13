package controller

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFullDutyHwMonFan returns a hwmon fan on pwm2 of a device with the given hwmon name and the it87 modalias, whose
// pwm2_enable reads 0 and pwm2 255.
func newFullDutyHwMonFan(t *testing.T, hwmonName string, quirksEnabled bool) (fans.Fan, string) {
	dir := t.TempDir()
	pwmPath := filepath.Join(dir, "pwm2")
	pwmEnablePath := filepath.Join(dir, "pwm2_enable")
	require.NoError(t, os.WriteFile(pwmPath, []byte("255"), 0o644))
	require.NoError(t, os.WriteFile(pwmEnablePath, []byte("0"), 0o644))
	config := configuration.FanConfig{
		ID: "front_chassis",
		HwMon: &configuration.HwMonFanConfig{
			PwmChannel: 2, PwmPath: pwmPath, PwmEnablePath: pwmEnablePath,
			RpmInputPath: filepath.Join(dir, "fan2_input"),
			HwmonName:    hwmonName, Modalias: "platform:it87",
		},
	}
	if !quirksEnabled {
		config.Quirks = &configuration.QuirksConfig{}
		config.Quirks.Enabled.SetOverride(false)
	}
	fan, err := fans.NewFan(config)
	require.NoError(t, err)
	return fan, pwmEnablePath
}

func TestEnsureFanModeIsSetToExpectedMode_FullDutyReadsAsFullSpeed(t *testing.T) {
	tests := []struct {
		name               string
		hwmonName          string
		quirksEnabled      bool
		expectedModeWrites bool
	}{
		// manual mode at pwm 255 reads 0 on this chip: nothing to restore
		{"it87 chip without fan on/off control", "it8665", true, false},
		// pwm_enable 0 means full speed without pwm control: fan2go switches to manual mode again
		{"it87 chip with fan on/off control", "it8728", true, true},
		{"quirks disabled", "it8665", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			fan, pwmEnablePath := newFullDutyHwMonFan(t, tt.hwmonName, tt.quirksEnabled)
			controller := DefaultFanController{fan: fan, clock: util.NewFakeClock(time.Unix(1000, 0))}

			// WHEN
			controller.ensureFanModeIsSetToExpectedMode()

			// THEN
			if tt.expectedModeWrites {
				assert.Equal(t, "1", readPwmEnableFile(t, pwmEnablePath))
			} else {
				assert.Equal(t, "0", readPwmEnableFile(t, pwmEnablePath))
			}
		})
	}
}
