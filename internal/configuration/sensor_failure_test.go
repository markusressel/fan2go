package configuration

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func loadYaml(t *testing.T, yaml string) Configuration {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	setDefaultValues()
	viper.SetConfigType("yaml")
	if err := viper.ReadConfig(strings.NewReader(yaml)); err != nil {
		t.Fatalf("invalid test YAML: %v", err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

func TestSensorStaleTimeout_Effective(t *testing.T) {
	tests := []struct {
		name    string
		globals string
		want    time.Duration
	}{
		{name: "unset, default polling rate", want: 10 * time.Second},
		{name: "unset, slow polling rate", globals: "tempSensorPollingRate: 2s", want: 20 * time.Second},
		{name: "explicit value overrides the default", globals: "tempSensorPollingRate: 2s\nsensorStaleTimeout: 5s", want: 5 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadYaml(t, validationGapsYaml(tt.globals, "", ""))
			assert.Equal(t, tt.want, cfg.GetSensorStaleTimeout())
		})
	}
}

func TestFanConfig_OnSensorFailure(t *testing.T) {
	cfg := loadYaml(t, validationGapsYaml("", "", ""))
	assert.Equal(t, OnSensorFailureMax, cfg.Fans[0].GetOnSensorFailure(), "defaults to max")

	cfg = loadYaml(t, validationGapsYaml("", "    onSensorFailure: restore", ""))
	assert.Equal(t, OnSensorFailureRestore, cfg.Fans[0].GetOnSensorFailure())
}
