package hwmon

import (
	"path/filepath"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeSysfsController returns a controller like GetChips for the fake hwmon device in testdata/sysfs/<device>,
// with one fan on channel 1. GetChips and GetFans need libsensors, so this fills HwmonName and Modalias the same
// way (getDeviceName, getDeviceModalias) instead of calling them.
func newFakeSysfsController(t *testing.T, device string) *HwMonController {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "sysfs", device))
	require.NoError(t, err)
	hwmonName := getDeviceName(path)
	modalias := getDeviceModalias(path)
	fan := &fans.HwMonFan{Config: configuration.FanConfig{HwMon: &configuration.HwMonFanConfig{
		Index: 1, RpmChannel: 1, PwmChannel: 1, SysfsPath: path, HwmonName: hwmonName, Modalias: modalias,
	}}}
	return &HwMonController{
		Name: device + "-virtual-0", HwmonName: hwmonName, Modalias: modalias, Platform: device + "-virtual-0",
		Path: path, Fans: []*fans.HwMonFan{fan},
	}
}

func TestKnownQuirks_FakeSysfsDevices(t *testing.T) {
	tests := []struct {
		device              string
		expectedHwmonName   string
		expectedQuirk       string
		expectControlModeRw bool
	}{
		{"corsaircpro", "corsaircpro", "corsaircpro", false},
		{"d5next", "d5next", "aquacomputer", false},
		{"thinkpad", "thinkpad", "thinkpad", true},
		{"nzxtsmart2", "nzxtsmart2", "nzxtsmart2", true},
		{"it8665", "it8665", "it87", true},
		{"nct6798", "nct6798", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.device, func(t *testing.T) {
			// GIVEN
			controller := newFakeSysfsController(t, tt.device)
			config := configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{Platform: tt.device, RpmChannel: 1}}

			// WHEN
			require.NoError(t, UpdateFanConfigFromHwMonControllers([]*HwMonController{controller}, &config))
			fan, err := fans.NewFan(config)
			require.NoError(t, err)

			// THEN: the raw hwmon name and the modalias are passed on to the fan config, and select the entry
			assert.Equal(t, tt.expectedHwmonName, config.HwMon.HwmonName)
			assert.Equal(t, controller.Modalias, config.HwMon.Modalias)
			assert.NotEmpty(t, config.HwMon.Modalias)
			entry := fans.KnownQuirksOf(fan)
			if tt.expectedQuirk == "" {
				assert.Nil(t, entry)
			} else {
				require.NotNil(t, entry)
				assert.Equal(t, tt.expectedQuirk, entry.ID)
			}
			assert.Equal(t, tt.expectControlModeRw, fan.Supports(fans.FeatureControlModeWrite))
		})
	}
}

func TestKnownQuirks_FakeSysfsDevice_DisabledInConfig(t *testing.T) {
	// GIVEN
	controller := newFakeSysfsController(t, "corsaircpro")
	quirksConfig := &configuration.QuirksConfig{}
	quirksConfig.Enabled.SetOverride(false)
	config := configuration.FanConfig{
		ID: "fan", HwMon: &configuration.HwMonFanConfig{Platform: "corsaircpro"}, Quirks: quirksConfig,
	}

	// WHEN
	require.NoError(t, UpdateFanConfigFromHwMonControllers([]*HwMonController{controller}, &config))
	fan, err := fans.NewFan(config)
	require.NoError(t, err)

	// THEN
	assert.Nil(t, fans.KnownQuirksOf(fan))
}

func TestKnownQuirks_FakeSysfsDevice_It87ManualModeAtFullDuty(t *testing.T) {
	// GIVEN: pwm1_enable reads 0 and pwm1 255, which is manual mode at full duty on this chip
	controller := newFakeSysfsController(t, "it8665")
	config := configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{Platform: "it8665", RpmChannel: 1}}
	require.NoError(t, UpdateFanConfigFromHwMonControllers([]*HwMonController{controller}, &config))
	fan, err := fans.NewFan(config)
	require.NoError(t, err)

	// WHEN
	mode, err := fan.GetControlMode()

	// THEN
	require.NoError(t, err)
	assert.Equal(t, fans.ControlModePWM, mode)
}
