package configuration

import (
	"reflect"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/stretchr/testify/assert"
)

func TestDefaultTrueBool_Get(t *testing.T) {
	tests := []struct {
		name     string
		input    DefaultTrueBool
		expected bool
	}{
		{
			name: "Present and True returns True",
			input: DefaultTrueBool{
				Optional: Optional[bool]{Value: true, Present: true},
			},
			expected: true,
		},
		{
			name: "Present and False returns False",
			input: DefaultTrueBool{
				Optional: Optional[bool]{Value: false, Present: true},
			},
			expected: false,
		},
		{
			name: "Not Present returns True (Default)",
			input: DefaultTrueBool{
				Optional: Optional[bool]{Value: false, Present: false},
			},
			expected: true,
		},
		{
			name: "Runtime Override wins over Missing",
			input: func() DefaultTrueBool {
				b := DefaultTrueBool{}
				b.SetOverride(false)
				return b
			}(),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.input.Get(); got != tt.expected {
				t.Errorf("DefaultTrueBool.Get() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDefaultTrueBoolHookFunc(t *testing.T) {
	type TestConfig struct {
		Enabled DefaultTrueBool `mapstructure:"enabled"`
	}

	tests := []struct {
		name          string
		inputMap      map[string]interface{}
		expectedValue bool
		expectedPres  bool
		expectedGet   bool
	}{
		{
			name:          "Explicit false in config",
			inputMap:      map[string]interface{}{"enabled": false},
			expectedValue: false,
			expectedPres:  true,
			expectedGet:   false,
		},
		{
			name:          "Explicit true in config",
			inputMap:      map[string]interface{}{"enabled": true},
			expectedValue: true,
			expectedPres:  true,
			expectedGet:   true,
		},
		{
			name:          "String 'false' in config",
			inputMap:      map[string]interface{}{"enabled": "false"},
			expectedValue: false,
			expectedPres:  true,
			expectedGet:   false,
		},
		{
			name:          "Missing from config (Zero Value)",
			inputMap:      map[string]interface{}{},
			expectedValue: false,
			expectedPres:  false,
			expectedGet:   true, // Inherited behavior
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg TestConfig

			decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
				DecodeHook: DefaultTrueBoolHookFunc(),
				Result:     &cfg,
			})
			if err != nil {
				t.Fatalf("failed to create decoder: %v", err)
			}

			err = decoder.Decode(tt.inputMap)
			if err != nil {
				t.Fatalf("decoding failed: %v", err)
			}

			if cfg.Enabled.Present != tt.expectedPres {
				t.Errorf("Present = %v, want %v", cfg.Enabled.Present, tt.expectedPres)
			}
			if cfg.Enabled.Value != tt.expectedValue {
				t.Errorf("Value = %v, want %v", cfg.Enabled.Value, tt.expectedValue)
			}
			if cfg.Enabled.Get() != tt.expectedGet {
				t.Errorf("Get() = %v, want %v", cfg.Enabled.Get(), tt.expectedGet)
			}
		})
	}
}

func TestHookSkipsUnrelatedTypes(t *testing.T) {
	hook := DefaultTrueBoolHookFunc()

	f := reflect.TypeOf("string")
	tTarget := reflect.TypeOf(123)
	data := "some string"

	res, err := hook(f, tTarget, data)

	if err != nil {
		t.Errorf("Hook returned error on unrelated type: %v", err)
	}
	if res != data {
		t.Errorf("Hook modified unrelated data. Got %v, want %v", res, data)
	}
}

// helper: run pwmMapPointsHookFunc for a given input and target type
func runPwmMapPointsHook(t *testing.T, data interface{}, target interface{}) (interface{}, error) {
	t.Helper()
	hook := pwmMapPointsHookFunc()
	from := reflect.TypeOf(data)
	to := reflect.TypeOf(target)
	return hook(from, to, data)
}

func TestPwmMapPointsHookFunc_LinearConfig(t *testing.T) {
	data := map[interface{}]interface{}{
		0:   0,
		255: 255,
	}
	result, err := runPwmMapPointsHook(t, data, PwmMapLinearConfig{})
	assert.NoError(t, err)
	cfg, ok := result.(PwmMapLinearConfig)
	assert.True(t, ok)
	assert.Equal(t, PwmMapLinearConfig{0: 0, 255: 255}, cfg)
}

func TestPwmMapPointsHookFunc_ValuesConfig(t *testing.T) {
	data := map[interface{}]interface{}{
		0:   0,
		128: 64,
		255: 100,
	}
	result, err := runPwmMapPointsHook(t, data, PwmMapValuesConfig{})
	assert.NoError(t, err)
	cfg, ok := result.(PwmMapValuesConfig)
	assert.True(t, ok)
	assert.Equal(t, PwmMapValuesConfig{0: 0, 128: 64, 255: 100}, cfg)
}

func TestPwmMapPointsHookFunc_LegacyBareMap_InterfaceKeys(t *testing.T) {
	// bare map with numeric keys → legacy compat → PwmMapConfig{Values: ...}
	data := map[interface{}]interface{}{
		0:   0,
		64:  128,
		192: 255,
	}
	result, err := runPwmMapPointsHook(t, data, PwmMapConfig{})
	assert.NoError(t, err)
	cfg, ok := result.(PwmMapConfig)
	assert.True(t, ok)
	assert.NotNil(t, cfg.Values)
	assert.Equal(t, PwmMapValuesConfig{0: 0, 64: 128, 192: 255}, *cfg.Values)
}

func TestPwmMapPointsHookFunc_LegacyBareMap_StringKeys(t *testing.T) {
	// old format with string-typed numeric keys (how viper sometimes delivers them)
	data := map[string]interface{}{
		"0":   0,
		"64":  128,
		"192": 255,
	}
	result, err := runPwmMapPointsHook(t, data, PwmMapConfig{})
	assert.NoError(t, err)
	cfg, ok := result.(PwmMapConfig)
	assert.True(t, ok)
	assert.NotNil(t, cfg.Values)
	assert.Equal(t, PwmMapValuesConfig{0: 0, 64: 128, 192: 255}, *cfg.Values)
}

func TestPwmMapPointsHookFunc_NonBareMap_PassThrough(t *testing.T) {
	// a map with a "linear" key should NOT be intercepted by the hook
	// (it will be decoded normally by mapstructure into PwmMapConfig.Linear)
	data := map[string]interface{}{
		"linear": map[interface{}]interface{}{0: 0, 255: 255},
	}
	result, err := runPwmMapPointsHook(t, data, PwmMapConfig{})
	assert.NoError(t, err)
	// should pass through unchanged
	assert.Equal(t, data, result)
}

func TestPwmMapUnmarshalText_Autodetect(t *testing.T) {
	var cfg PwmMapConfig
	err := cfg.UnmarshalText([]byte("autodetect"))
	assert.NoError(t, err)
	assert.NotNil(t, cfg.Autodetect)
	assert.Nil(t, cfg.Identity)
	assert.Nil(t, cfg.Linear)
	assert.Nil(t, cfg.Values)
}

func TestPwmMapUnmarshalText_Identity(t *testing.T) {
	var cfg PwmMapConfig
	err := cfg.UnmarshalText([]byte("identity"))
	assert.NoError(t, err)
	assert.Nil(t, cfg.Autodetect)
	assert.NotNil(t, cfg.Identity)
	assert.Nil(t, cfg.Linear)
	assert.Nil(t, cfg.Values)
}

func TestPwmMapUnmarshalText_Unknown(t *testing.T) {
	var cfg PwmMapConfig
	err := cfg.UnmarshalText([]byte("bogus"))
	assert.Error(t, err)
}
