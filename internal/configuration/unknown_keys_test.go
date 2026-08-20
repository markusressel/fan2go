package configuration

import (
	"reflect"
	"testing"
)

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"fan", "fans", 1},
		{"platorm", "platform", 1},
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

	expected := []string{"dbpath", "runfaninitializationinparallel", "maxrpmdiffforsettledfan", "fanresponsedelay", "tempsensorpollingrate", "temprollingwindowsize", "rpmpollingrate", "rpmrollingwindowsize", "controlleradjustmenttickrate", "analysis", "fancontroller", "fans", "sensors", "curves", "api", "statistics", "profiling"}

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
