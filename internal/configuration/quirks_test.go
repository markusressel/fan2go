package configuration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuirksConfig_Parse(t *testing.T) {
	cfg := loadYaml(t, `
fans:
  - id: default
    hwMon: { platform: corsaircpro, rpmChannel: 1 }
    curve: c
  - id: disabled
    hwMon: { platform: corsaircpro, rpmChannel: 2 }
    curve: c
    quirks:
      enabled: false
  - id: enabled
    hwMon: { platform: corsaircpro, rpmChannel: 3 }
    curve: c
    quirks:
      enabled: true
`)
	require.Len(t, cfg.Fans, 3)
	assert.Nil(t, cfg.Fans[0].Quirks)
	assert.True(t, cfg.Fans[0].QuirksEnabled())
	require.NotNil(t, cfg.Fans[1].Quirks)
	assert.False(t, cfg.Fans[1].QuirksEnabled())
	assert.True(t, cfg.Fans[2].QuirksEnabled())
}

func TestQuirksConfig_OnlyForHwmonFans(t *testing.T) {
	config := Configuration{
		Curves: []CurveConfig{{ID: "c", Linear: &LinearCurveConfig{Sensor: "s"}}},
		Fans: []FanConfig{{
			ID: "file", File: &FileFanConfig{Path: "/tmp/fan"}, Curve: "c", Quirks: &QuirksConfig{},
		}},
	}
	err := validateFans(&config)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quirks can only be configured for hwmon fans")
}
