package configuration

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/pterm/pterm"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureWarnings redirects the warnings logged during the test into the returned buffer.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var warnings bytes.Buffer
	oldWarning := pterm.Warning
	pterm.Warning = *pterm.Warning.WithWriter(&warnings)
	t.Cleanup(func() { pterm.Warning = oldWarning })
	return &warnings
}

func loadFanConfigFromYaml(t *testing.T, fanYaml string) FanConfig {
	t.Helper()
	cfg := loadConfigFromYaml(t, `
fans:
  - id: fan
    curve: curve
    file:
      path: /tmp/pwm
`+fanYaml)
	require.Len(t, cfg.Fans, 1)
	return cfg.Fans[0]
}

func TestCurveUnits_UnmarshalText(t *testing.T) {
	tests := []struct {
		text     string
		expected float64
	}{
		{"10", 10},
		{"2.5", 2.5},
		{" 7 ", 7},
		// percent of the curve value range, linear: 1% = 2.55 (unlike curve steps, where 1% = 1)
		{"1%", 2.55},
		{"10%", 25.5},
		{"0.5%", 1.275},
		{"100%", 255},
		{"0%", 0},
		{"-1%", -2.55},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			var value CurveUnits
			require.NoError(t, value.UnmarshalText([]byte(tt.text)))
			assert.InDelta(t, tt.expected, value.Value(), 1e-12)
		})
	}
}

func TestCurveUnits_UnmarshalText_Invalid(t *testing.T) {
	for _, text := range []string{"", "%", "abc", "10 %%", "NaN", "Inf%", "1e999"} {
		var value CurveUnits
		assert.Error(t, value.UnmarshalText([]byte(text)), text)
	}
}

func TestCurveUnits_UnmarshalJSON(t *testing.T) {
	var config DirectControlAlgorithmConfig
	require.NoError(t, json.Unmarshal([]byte(`{"rampUp": 10, "rampDown": "2%", "deadband": "1.5"}`), &config))
	assert.Equal(t, CurveUnits(10), config.RampUp)
	assert.InDelta(t, 5.1, config.RampDown.Value(), 1e-12)
	assert.Equal(t, CurveUnits(1.5), config.Deadband)

	assert.Error(t, json.Unmarshal([]byte(`{"rampUp": true}`), &config))
}

func TestLoadConfig_DirectControlAlgorithmOptions(t *testing.T) {
	// WHEN
	fan := loadFanConfigFromYaml(t, `
    controlAlgorithm:
      direct:
        timeConstant: 2500ms
        rampUp: 10%
        rampDown: 3
        deadband: "0.5%"
`)

	// THEN
	require.NotNil(t, fan.ControlAlgorithm)
	require.NotNil(t, fan.ControlAlgorithm.Direct)
	direct := fan.ControlAlgorithm.Direct
	assert.Equal(t, 2500*time.Millisecond, direct.TimeConstant.Duration())
	assert.InDelta(t, 25.5, direct.RampUp.Value(), 1e-12)
	assert.Equal(t, CurveUnits(3), direct.RampDown)
	assert.InDelta(t, 1.275, direct.Deadband.Value(), 1e-12)
	assert.Nil(t, direct.MaxPwmChangePerCycle) //nolint:staticcheck // deprecated, but still supported
}

func TestLoadConfig_DirectTimeConstant_PlainNumberIsSeconds(t *testing.T) {
	for yaml, expected := range map[string]time.Duration{
		"2":     2 * time.Second,
		"2.5":   2500 * time.Millisecond,
		`"1.5"`: 1500 * time.Millisecond,
		"500ms": 500 * time.Millisecond,
		"1m":    time.Minute,
		"0":     0,
	} {
		t.Run(yaml, func(t *testing.T) {
			fan := loadFanConfigFromYaml(t, "    controlAlgorithm:\n      direct:\n        timeConstant: "+yaml+"\n")
			assert.Equal(t, expected, fan.ControlAlgorithm.Direct.TimeConstant.Duration())
		})
	}
}

func TestLoadConfig_DirectControlAlgorithm_InvalidPercent(t *testing.T) {
	viperYaml := `
fans:
  - id: fan
    curve: curve
    file:
      path: /tmp/pwm
    controlAlgorithm:
      direct:
        rampUp: fast
`
	loadConfigFromYamlExpectingError(t, viperYaml)
}

func TestLoadConfig_ExplicitDirectIsAPassThrough(t *testing.T) {
	for name, yaml := range map[string]string{
		"string form": "    controlAlgorithm: direct\n",
		"object form": "    controlAlgorithm:\n      direct: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			fan := loadFanConfigFromYaml(t, yaml)

			assert.Equal(t, ControlAlgorithmConfig{Direct: &DirectControlAlgorithmConfig{}}, fan.GetControlAlgorithm())
		})
	}
}

func TestValidateFans_EmptyControlAlgorithmIsRejected(t *testing.T) {
	for name, yaml := range map[string]string{
		"direct without a value": "    controlAlgorithm:\n      direct:\n",
		"empty object":           "    controlAlgorithm: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			fan := loadFanConfigFromYaml(t, yaml)
			config := newDirectValidationConfig(DirectControlAlgorithmConfig{})
			config.Fans[0].ControlAlgorithm = fan.ControlAlgorithm

			assert.ErrorContains(t, validateFans(config), "fan fan: controlAlgorithm is empty, use one of: direct | pid")
		})
	}
}

func TestGetControlAlgorithm_DefaultIsDirectWithTimeConstantAndDeadband(t *testing.T) {
	// GIVEN: a fan without controlAlgorithm
	fan := loadFanConfigFromYaml(t, "")

	// WHEN
	algorithm := fan.GetControlAlgorithm()

	// THEN
	assert.Nil(t, algorithm.Pid)
	require.NotNil(t, algorithm.Direct)
	assert.Equal(t, 2*time.Second, algorithm.Direct.TimeConstant.Duration())
	assert.InDelta(t, 2.55, algorithm.Direct.Deadband.Value(), 1e-12)
	assert.Zero(t, algorithm.Direct.RampUp)
	assert.Zero(t, algorithm.Direct.RampDown)
	assert.Nil(t, algorithm.Direct.MaxPwmChangePerCycle) //nolint:staticcheck // deprecated, but still supported
}

func TestGetControlAlgorithm_DefaultIsNotShared(t *testing.T) {
	first := FanConfig{}.GetControlAlgorithm()
	first.Direct.Deadband = 10

	assert.Equal(t, DefaultDirectDeadband, FanConfig{}.GetControlAlgorithm().Direct.Deadband)
}

func TestGetControlAlgorithm_Pid(t *testing.T) {
	fan := loadFanConfigFromYaml(t, "    controlAlgorithm: pid\n")

	algorithm := fan.GetControlAlgorithm()

	assert.Nil(t, algorithm.Direct)
	assert.Equal(t, &PidControlAlgorithmConfig{P: 0.05, I: 0.4, D: 0.01}, algorithm.Pid)
}

func newDirectValidationConfig(direct DirectControlAlgorithmConfig) *Configuration {
	return &Configuration{
		FanController: FanControllerConfig{AdjustmentTickRate: 200 * time.Millisecond},
		Curves:        []CurveConfig{{ID: "curve", Linear: &LinearCurveConfig{Sensor: "sensor", Min: 0, Max: 100}}},
		Fans: []FanConfig{{
			ID: "fan", Curve: "curve", File: &FileFanConfig{Path: "/tmp/pwm"},
			ControlAlgorithm: &ControlAlgorithmConfig{Direct: &direct},
		}},
	}
}

func TestValidateDirectControlAlgorithm_Errors(t *testing.T) {
	maxChange := 10
	zero := 0
	tests := []struct {
		name   string
		direct DirectControlAlgorithmConfig
		err    string
	}{
		{"negative timeConstant", DirectControlAlgorithmConfig{TimeConstant: SecondsOrDuration(-time.Second)},
			"fan fan: controlAlgorithm.direct.timeConstant must be >= 0, got -1s"},
		{"negative rampUp", DirectControlAlgorithmConfig{RampUp: -1},
			"fan fan: controlAlgorithm.direct.rampUp must be >= 0, got -1"},
		{"negative rampDown", DirectControlAlgorithmConfig{RampDown: -2.55},
			"fan fan: controlAlgorithm.direct.rampDown must be >= 0, got -2.55"},
		{"infinite rampDown", DirectControlAlgorithmConfig{RampDown: CurveUnits(math.Inf(1))},
			"fan fan: controlAlgorithm.direct.rampDown must be a finite number, got +Inf"},
		{"NaN deadband", DirectControlAlgorithmConfig{Deadband: CurveUnits(math.NaN())},
			"fan fan: controlAlgorithm.direct.deadband must be a finite number, got NaN"},
		{"negative deadband", DirectControlAlgorithmConfig{Deadband: -1},
			"fan fan: controlAlgorithm.direct.deadband must be >= 0, got -1"},
		{"deadband of 100%", DirectControlAlgorithmConfig{Deadband: 255},
			"fan fan: controlAlgorithm.direct.deadband must be < 255 (100%), got 255"},
		{"maxPwmChangePerCycle 0", DirectControlAlgorithmConfig{MaxPwmChangePerCycle: &zero},
			"fan fan: invalid maxPwmChangePerCycle, must be > 0"},
		{"maxPwmChangePerCycle with rampUp", DirectControlAlgorithmConfig{MaxPwmChangePerCycle: &maxChange, RampUp: 10},
			"fan fan: controlAlgorithm.direct.maxPwmChangePerCycle can't be combined with rampUp or rampDown, remove the deprecated maxPwmChangePerCycle"},
		{"maxPwmChangePerCycle with rampDown", DirectControlAlgorithmConfig{MaxPwmChangePerCycle: &maxChange, RampDown: 10},
			"fan fan: controlAlgorithm.direct.maxPwmChangePerCycle can't be combined with rampUp or rampDown, remove the deprecated maxPwmChangePerCycle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.EqualError(t, validateFans(newDirectValidationConfig(tt.direct)), tt.err)
		})
	}
}

func TestValidateDirectControlAlgorithm_Valid(t *testing.T) {
	maxChange := 10
	for name, direct := range map[string]DirectControlAlgorithmConfig{
		"pass-through":                     {},
		"default":                          *DefaultControlAlgorithmConfig().Direct,
		"all options":                      {TimeConstant: SecondsOrDuration(5 * time.Second), RampUp: 25.5, RampDown: 5.1, Deadband: 5.1},
		"maxPwmChangePerCycle with others": {MaxPwmChangePerCycle: &maxChange, TimeConstant: SecondsOrDuration(time.Second), Deadband: 2.55},
	} {
		t.Run(name, func(t *testing.T) {
			warnings := captureWarnings(t)

			assert.NoError(t, validateFans(newDirectValidationConfig(direct)))
			if direct.MaxPwmChangePerCycle == nil { //nolint:staticcheck // deprecated, but still supported
				assert.Empty(t, warnings.String())
			}
		})
	}
}

func TestValidateDirectControlAlgorithm_MaxPwmChangePerCycleIsDeprecated(t *testing.T) {
	// GIVEN
	maxChange := 10
	warnings := captureWarnings(t)

	// WHEN
	err := validateFans(newDirectValidationConfig(DirectControlAlgorithmConfig{MaxPwmChangePerCycle: &maxChange}))

	// THEN: still valid, with a warning that names the equivalent per second at the current tick rate
	assert.NoError(t, err)
	assert.Contains(t, warnings.String(), "fan fan: controlAlgorithm.direct.maxPwmChangePerCycle is deprecated")
	assert.Contains(t, warnings.String(), "With the current adjustmentTickRate of 200ms, 10 per cycle is 50 per second: use rampUp: 50 and rampDown: 50 instead")
}

func TestValidateDirectControlAlgorithm_Warnings(t *testing.T) {
	tests := []struct {
		name    string
		direct  DirectControlAlgorithmConfig
		warning string
	}{
		{"tiny timeConstant", DirectControlAlgorithmConfig{TimeConstant: SecondsOrDuration(2 * time.Millisecond)},
			`fan fan: controlAlgorithm.direct.timeConstant is 2ms, which smooths almost nothing (a plain number is seconds, e.g. 2 or "2s")`},
		{"slow timeConstant", DirectControlAlgorithmConfig{TimeConstant: SecondsOrDuration(2 * time.Minute)},
			"fan fan: controlAlgorithm.direct.timeConstant is 2m0s, the fan will react very slowly to temperature changes"},
		{"slow rampUp", DirectControlAlgorithmConfig{RampUp: 2},
			"fan fan: controlAlgorithm.direct.rampUp is 2 per second, so a full increase of the fan speed takes 2m8s, which delays cooling"},
		{"large deadband", DirectControlAlgorithmConfig{Deadband: 30},
			"fan fan: controlAlgorithm.direct.deadband is 30 (more than 10%), the fan speed may stay noticeably off the curve"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := captureWarnings(t)

			assert.NoError(t, validateFans(newDirectValidationConfig(tt.direct)))
			assert.Contains(t, warnings.String(), tt.warning)
		})
	}
}

// loadConfigFromYamlExpectingError loads the given YAML through the real LoadConfig and expects it to fail.
func loadConfigFromYamlExpectingError(t *testing.T, yaml string) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	setDefaultValues()
	viper.SetConfigType("yaml")
	require.NoError(t, viper.ReadConfig(bytes.NewBufferString(yaml)))
	_, err := LoadConfig()
	assert.Error(t, err)
}
