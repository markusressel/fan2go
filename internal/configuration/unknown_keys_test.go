package configuration

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"fan", "fans", 1},
		{"platorm", "platform", 1}, // codespell:ignore
		{"sensors", "sensor", 1},
		{"same", "same", 0},
	}

	for _, tt := range tests {
		if got := levenshtein(tt.a, tt.b); got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestGetClosestMatch(t *testing.T) {
	tests := []struct {
		target  string
		options []string
		want    string
	}{
		{"fan", []string{"fans", "sensors"}, "fans"},
		{"plattorm", []string{"platform", "index"}, "platform"},
		{"xyz", []string{"platform", "index"}, ""}, // too far
		{"ab", []string{"abc", "def"}, ""},         // too short
	}

	for _, tt := range tests {
		if got := getClosestMatch(tt.target, tt.options); got != tt.want {
			t.Errorf("getClosestMatch(%q, %v) = %q, want %q", tt.target, tt.options, got, tt.want)
		}
	}
}

func TestGetValidKeys(t *testing.T) {
	cfg := Configuration{}
	keys := getValidKeys(reflect.TypeOf(cfg))

	expected := []string{"dbpath", "runfaninitializationinparallel", "maxrpmdiffforsettledfan", "fanresponsedelay", "tempsensorpollingrate", "temprollingwindowsize", "sensorstaletimeout", "rpmpollingrate", "rpmrollingwindowsize", "controlleradjustmenttickrate", "analysis", "fancontroller", "fans", "sensors", "curves", "api", "statistics", "profiling"}

	if len(keys) != len(expected) {
		t.Errorf("getValidKeys() returned %d keys, want %d", len(keys), len(expected))
	}

	// simple check for some known keys
	foundFans := false
	for _, k := range keys {
		if k == "fans" {
			foundFans = true
			break
		}
	}
	if !foundFans {
		t.Errorf("getValidKeys() missing 'fans'")
	}
}

func TestGetParentType(t *testing.T) {
	cfg := Configuration{}

	// Root level
	typ := getParentType([]string{}, reflect.TypeOf(cfg))
	if typ != reflect.TypeOf(cfg) {
		t.Errorf("getParentType() for root failed")
	}

	// Fans slice element type (FanConfig)
	typ = getParentType([]string{"fans", "0"}, reflect.TypeOf(cfg))
	if typ.Name() != "FanConfig" {
		t.Errorf("getParentType() for fans.0 = %v, want FanConfig", typ)
	}

	// HwMon config inside fan
	typ = getParentType([]string{"fans", "0", "hwmon"}, reflect.TypeOf(cfg))
	if typ.Name() != "HwMonFanConfig" {
		t.Errorf("getParentType() for fans.0.hwmon = %v, want HwMonFanConfig", typ)
	}

	// Invalid path
	typ = getParentType([]string{"invalid", "path"}, reflect.TypeOf(cfg))
	if typ != nil {
		t.Errorf("getParentType() for invalid path should be nil, got %v", typ)
	}
}

func TestLoadConfig_DotTemplatesSectionWithAnchorsIsIgnored(t *testing.T) {
	// GIVEN a top-level ".templates" section that only holds YAML anchors
	warnings := captureWarnings(t)

	// WHEN
	cfg := loadConfigFromYaml(t, `
.templates:
  x-values-gpu-set-pwm-to-get-pwm-map: &x-values-gpu-set-pwm-to-get-pwm-map
    values:
      0: 0
      255: 255
fans:
  - id: gpu
    setPwmToGetPwmMap: *x-values-gpu-set-pwm-to-get-pwm-map
`)

	// THEN there is no warning and the anchor is resolved
	assert.NotContains(t, warnings.String(), "Unknown configuration")
	require.Len(t, cfg.Fans, 1)
	require.NotNil(t, cfg.Fans[0].SetPwmToGetPwmMap)
	require.NotNil(t, cfg.Fans[0].SetPwmToGetPwmMap.Values)
	assert.Equal(t, SetPwmToGetPwmMapValuesConfig{0: 0, 255: 255}, *cfg.Fans[0].SetPwmToGetPwmMap.Values)
}

func TestLoadConfig_XPrefixedTopLevelSectionIsIgnored(t *testing.T) {
	// GIVEN top-level "x-" keys that hold YAML anchors
	warnings := captureWarnings(t)

	// WHEN
	cfg := loadConfigFromYaml(t, `
x-templates:
  pwm-map: &pwm-map
    linear:
      0: 0
      255: 255
x-scalar: 1
fans:
  - id: gpu
    setPwmToGetPwmMap: *pwm-map
`)

	// THEN
	assert.NotContains(t, warnings.String(), "Unknown configuration")
	require.Len(t, cfg.Fans, 1)
	require.NotNil(t, cfg.Fans[0].SetPwmToGetPwmMap)
	assert.NotNil(t, cfg.Fans[0].SetPwmToGetPwmMap.Linear)
}

func TestLoadConfig_UnknownTopLevelKeyIsWarned(t *testing.T) {
	// GIVEN
	warnings := captureWarnings(t)

	// WHEN
	loadConfigFromYaml(t, `
templates:
  a: 1
fooBar: 1
`)

	// THEN
	assert.Contains(t, warnings.String(), "Unknown configuration section (and its contents): templates")
	assert.Contains(t, warnings.String(), "Unknown configuration key: foobar")
}

func TestLoadConfig_UnknownNestedKeyIsWarned(t *testing.T) {
	// GIVEN keys starting with "x-" are only ignored at the top level
	warnings := captureWarnings(t)

	// WHEN
	loadConfigFromYaml(t, `
fans:
  - id: gpu
    unknownOption: 1
    x-foo: 1
`)

	// THEN
	assert.Contains(t, warnings.String(), "Unknown configuration key: fans[0].unknownoption")
	assert.Contains(t, warnings.String(), "Unknown configuration key: fans[0].x-foo")
}

func TestWarnUnknownKeys_EmptyKeyNameIsPrinted(t *testing.T) {
	// GIVEN a nested key whose last path segment is empty
	warnings := captureWarnings(t)

	// WHEN
	WarnUnknownKeys(&Configuration{}, []string{"Fans[0]."}, func(string) interface{} { return 1 })

	// THEN
	assert.Contains(t, warnings.String(), "Unknown configuration key: fans[0].''")
}

func TestIsIgnoredTopLevelKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"", true}, // viper splits ".templates" into "" and "templates"
		{"x-templates", true},
		{"x-", true},
		{"templates", false},
		{"fans[0].x-foo", false},
		{"fans[0].", false},
		{"curves[1]", false},
	}

	for _, tt := range tests {
		if got := isIgnoredTopLevelKey(tt.key); got != tt.want {
			t.Errorf("isIgnoredTopLevelKey(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}
