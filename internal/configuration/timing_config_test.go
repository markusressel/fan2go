package configuration

import (
	"bytes"
	"math"
	"testing"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func decodeSecondsOrDuration(t *testing.T, input interface{}) (SecondsOrDuration, error) {
	t.Helper()
	type testConfig struct {
		Delay SecondsOrDuration `mapstructure:"delay"`
	}
	var cfg testConfig
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		// same hook order as LoadConfig
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			secondsOrDurationHookFunc(),
			durationMillisecondsHookFunc(),
			mapstructure.StringToTimeDurationHookFunc(),
		),
		Result: &cfg,
	})
	assert.NoError(t, err)
	err = decoder.Decode(map[string]interface{}{"delay": input})
	return cfg.Delay, err
}

func TestSecondsOrDurationHookFunc(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected time.Duration
	}{
		{name: "int is seconds", input: 2, expected: 2 * time.Second},
		{name: "int64 is seconds", input: int64(3), expected: 3 * time.Second},
		{name: "uint is seconds", input: uint(4), expected: 4 * time.Second},
		{name: "float is seconds", input: 0.5, expected: 500 * time.Millisecond},
		{name: "numeric string is seconds", input: "2", expected: 2 * time.Second},
		{name: "duration string with seconds", input: "2s", expected: 2 * time.Second},
		{name: "duration string with milliseconds", input: "500ms", expected: 500 * time.Millisecond},
		{name: "zero", input: 0, expected: 0},
		{name: "time.Duration is kept", input: 750 * time.Millisecond, expected: 750 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := decodeSecondsOrDuration(t, tt.input)
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, value.Duration())
		})
	}
}

func TestSecondsOrDurationHookFunc_InvalidValues(t *testing.T) {
	for _, input := range []interface{}{"soon", "NaN", "Inf", "1e30", math.NaN(), math.Inf(1), 1e30, uint64(math.MaxUint64)} {
		_, err := decodeSecondsOrDuration(t, input)
		assert.Error(t, err, "input %v", input)
	}
}

func TestLoadConfig_FanResponseDelayFromFlagAndEnv(t *testing.T) {
	tests := []struct {
		name     string
		flag     *string
		env      *string
		expected time.Duration
	}{
		{name: "flag with plain number is seconds", flag: ptr("3"), expected: 3 * time.Second},
		{name: "flag with duration", flag: ptr("500ms"), expected: 500 * time.Millisecond},
		{name: "flag default is 2 seconds", expected: 2 * time.Second},
		{name: "env with plain number is seconds", env: ptr("4"), expected: 4 * time.Second},
		{name: "env with duration", env: ptr("750ms"), expected: 750 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			setDefaultValues()
			// same flag definition as `fan2go fan init`
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			flags.StringP("fan-response-delay", "e", "2", "")
			assert.NoError(t, viper.BindPFlag("FanResponseDelay", flags.Lookup("fan-response-delay")))
			if tt.flag != nil {
				assert.NoError(t, flags.Parse([]string{"--fan-response-delay", *tt.flag}))
			}
			if tt.env != nil {
				t.Setenv("FANRESPONSEDELAY", *tt.env)
				viper.AutomaticEnv()
			}

			cfg, err := LoadConfig()

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, cfg.FanResponseDelay.Duration())
		})
	}
}

func ptr(s string) *string { return &s }

func TestSecondsOrDuration_String(t *testing.T) {
	assert.Equal(t, "500ms", SecondsOrDuration(500*time.Millisecond).String())
}

// loadConfigFromYaml loads the given YAML through the real LoadConfig (with defaults and decode hooks).
func loadConfigFromYaml(t *testing.T, yaml string) Configuration {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	setDefaultValues()
	viper.SetConfigType("yaml")
	assert.NoError(t, viper.ReadConfig(bytes.NewBufferString(yaml)))
	cfg, err := LoadConfig()
	assert.NoError(t, err)
	return cfg
}

func TestLoadConfig_FanResponseDelay(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		expected time.Duration
	}{
		{name: "default is 2 seconds", yaml: "dbPath: /tmp/x.db\n", expected: 2 * time.Second},
		{name: "plain number is seconds (backwards compatible)", yaml: "fanResponseDelay: 2\n", expected: 2 * time.Second},
		{name: "duration string with seconds", yaml: "fanResponseDelay: 2s\n", expected: 2 * time.Second},
		{name: "duration string with milliseconds", yaml: "fanResponseDelay: 500ms\n", expected: 500 * time.Millisecond},
		{name: "fraction of seconds", yaml: "fanResponseDelay: 0.5\n", expected: 500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadConfigFromYaml(t, tt.yaml)
			assert.Equal(t, tt.expected, cfg.FanResponseDelay.Duration())
		})
	}
}

func TestLoadConfig_CmdTimeout(t *testing.T) {
	cfg := loadConfigFromYaml(t, `
fans:
  - id: fan
    curve: curve
    cmd:
      setPwm:
        exec: /usr/bin/true
        timeout: 5s
      getPwm:
        exec: /usr/bin/true
sensors:
  - id: sensor
    cmd:
      exec: /usr/bin/true
      timeout: 500
`)

	assert.Len(t, cfg.Fans, 1)
	assert.Equal(t, 5*time.Second, cfg.Fans[0].Cmd.SetPwm.GetTimeout())
	// not configured: default
	assert.Equal(t, DefaultCmdTimeout, cfg.Fans[0].Cmd.GetPwm.GetTimeout())
	assert.Len(t, cfg.Sensors, 1)
	// plain numbers are milliseconds, like for every other time.Duration option
	assert.Equal(t, 500*time.Millisecond, cfg.Sensors[0].Cmd.GetTimeout())
}

func TestValidateSensors_NegativeCmdTimeout(t *testing.T) {
	config := Configuration{
		Sensors: []SensorConfig{
			{ID: "sensor", Cmd: &CmdSensorConfig{Exec: "/usr/bin/true", Timeout: -time.Second}},
		},
	}

	err := validateSensors(&config)

	assert.EqualError(t, err, "sensor sensor: cmd.timeout must be >= 0, got -1s")
}

func TestValidateSensors_CmdTimeoutUnsetOrPositive(t *testing.T) {
	for _, timeout := range []time.Duration{0, 500 * time.Millisecond} {
		config := Configuration{
			Sensors: []SensorConfig{
				{ID: "sensor", Cmd: &CmdSensorConfig{Exec: "/usr/bin/true", Timeout: timeout}},
			},
		}

		assert.NoError(t, validateSensors(&config))
	}
}

func TestValidateFans_NegativeCmdTimeout(t *testing.T) {
	newConfig := func(cmd *CmdFanConfig) *Configuration {
		return &Configuration{
			Curves: []CurveConfig{{ID: "curve", Linear: &LinearCurveConfig{Sensor: "sensor", Min: 0, Max: 100}}},
			Fans:   []FanConfig{{ID: "fan", Curve: "curve", Cmd: cmd}},
		}
	}
	valid := func() *ExecConfig { return &ExecConfig{Exec: "/usr/bin/true"} }
	negative := func() *ExecConfig { return &ExecConfig{Exec: "/usr/bin/true", Timeout: -time.Millisecond} }

	assert.NoError(t, validateFans(newConfig(&CmdFanConfig{SetPwm: valid(), GetPwm: valid(), GetRpm: valid()})))
	assert.EqualError(t, validateFans(newConfig(&CmdFanConfig{SetPwm: negative()})),
		"fan fan: cmd.setPwm.timeout must be >= 0, got -1ms")
	assert.EqualError(t, validateFans(newConfig(&CmdFanConfig{SetPwm: valid(), GetPwm: negative()})),
		"fan fan: cmd.getPwm.timeout must be >= 0, got -1ms")
	assert.EqualError(t, validateFans(newConfig(&CmdFanConfig{SetPwm: valid(), GetRpm: negative()})),
		"fan fan: cmd.getRpm.timeout must be >= 0, got -1ms")
}

func TestCmdTimeoutDefault(t *testing.T) {
	assert.Equal(t, 2*time.Second, DefaultCmdTimeout)
	assert.Equal(t, DefaultCmdTimeout, (&ExecConfig{}).GetTimeout())
	assert.Equal(t, DefaultCmdTimeout, (&CmdSensorConfig{}).GetTimeout())
	assert.Equal(t, time.Second, (&ExecConfig{Timeout: time.Second}).GetTimeout())
}
