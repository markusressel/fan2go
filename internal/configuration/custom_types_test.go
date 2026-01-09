package configuration

import (
	"reflect"
	"testing"

	"github.com/go-viper/mapstructure/v2"
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
