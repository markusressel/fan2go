package config

import (
	"errors"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/quirks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeController(platform string, hwmonName string) *hwmon.HwMonController {
	fan := &fans.HwMonFan{Config: configuration.FanConfig{HwMon: &configuration.HwMonFanConfig{
		Index: 1, RpmChannel: 1, PwmChannel: 1, SysfsPath: "/sys/class/hwmon/" + platform, HwmonName: hwmonName,
	}}}
	return &hwmon.HwMonController{Name: platform, Platform: platform, HwmonName: hwmonName, Fans: []*fans.HwMonFan{fan}}
}

func noParameter(string) ([]byte, error) { return nil, errors.New("not loaded") }

func TestKnownQuirksAdvice(t *testing.T) {
	// GIVEN: a ThinkPad, a Corsair Commander Pro and an nct6798 chip
	controllers := []*hwmon.HwMonController{
		fakeController("thinkpad-isa-0000", "thinkpad"),
		fakeController("corsaircpro-hid-3-1", "corsaircpro"),
		fakeController("nct6798-isa-0290", "nct6798"),
	}
	identity := &configuration.SetPwmToGetPwmMapConfig{Identity: &configuration.SetPwmToGetPwmMapIdentityConfig{}}
	disabledQuirks := &configuration.QuirksConfig{}
	disabledQuirks.Enabled.SetOverride(false)
	config := configuration.Configuration{
		FanController: configuration.FanControllerConfig{AdjustmentTickRate: 200 * time.Millisecond},
		Fans: []configuration.FanConfig{
			{ID: "cpu", HwMon: &configuration.HwMonFanConfig{Platform: "thinkpad"},
				ControlAlgorithm: &configuration.ControlAlgorithmConfig{Direct: &configuration.DirectControlAlgorithmConfig{Deadband: 2.55}}},
			{ID: "front", HwMon: &configuration.HwMonFanConfig{Platform: "corsaircpro"}, SetPwmToGetPwmMap: identity},
			// no entry, not found, quirks disabled, not hwmon: no advice
			{ID: "case", HwMon: &configuration.HwMonFanConfig{Platform: "nct6798"}, SetPwmToGetPwmMap: identity},
			{ID: "missing", HwMon: &configuration.HwMonFanConfig{Platform: "d5next"}, SetPwmToGetPwmMap: identity},
			{ID: "rear", HwMon: &configuration.HwMonFanConfig{Platform: "corsaircpro"}, SetPwmToGetPwmMap: identity, Quirks: disabledQuirks},
			{ID: "file", File: &configuration.FileFanConfig{Path: "/tmp/x"}, SetPwmToGetPwmMap: identity},
		},
	}

	// WHEN
	advice := knownQuirksAdvice(controllers, config, noParameter)

	// THEN
	var texts []string
	for _, a := range advice {
		texts = append(texts, a.Message)
	}
	require.Len(t, advice, 3, "%v", texts)
	assert.Contains(t, texts[0], "fan cpu: controlAlgorithm.direct.deadband")
	assert.Contains(t, texts[1], "fan cpu: the thinkpad_acpi driver only allows fan control")
	assert.Contains(t, texts[2], "fan front: setPwmToGetPwmMap is set")
	for _, a := range advice {
		assert.Equal(t, quirks.AdviceTip, a.Level)
	}
	// the config itself is not changed
	assert.Equal(t, "", config.Fans[0].HwMon.HwmonName)
	assert.Equal(t, 0, config.Fans[0].HwMon.Index)
}

func TestKnownQuirksAdvice_NoHardware(t *testing.T) {
	config := configuration.Configuration{Fans: []configuration.FanConfig{
		{ID: "cpu", HwMon: &configuration.HwMonFanConfig{Platform: "thinkpad"}},
	}}
	assert.Empty(t, knownQuirksAdvice(nil, config, noParameter))
}
