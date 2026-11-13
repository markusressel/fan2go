package configuration

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFanConfig_GetKickStart_Defaults(t *testing.T) {
	// no kickStart: mode auto, duration 0 (derived)
	kick := FanConfig{}.GetKickStart()
	assert.Equal(t, KickStartAuto, kick.Mode)
	assert.Zero(t, kick.Duration)
	assert.Nil(t, kick.Pwm)

	// configured values are kept, the pwm is a copy
	pwm := 150
	config := FanConfig{KickStart: &KickStartConfig{Mode: KickStartFixed, Pwm: &pwm, Duration: SecondsOrDuration(2 * time.Second)}}
	kick = config.GetKickStart()
	assert.Equal(t, KickStartFixed, kick.Mode)
	assert.Equal(t, SecondsOrDuration(2*time.Second), kick.Duration)
	*kick.Pwm = 1
	assert.Equal(t, 150, pwm)
}

func TestFanConfig_KickStart_IsDecodedFromYaml(t *testing.T) {
	yaml := validationGapsYaml("", "    kickStart:\n      mode: fixed\n      pwm: 120\n      duration: 2.5", "")
	viper.Reset()
	t.Cleanup(viper.Reset)
	setDefaultValues()
	viper.SetConfigType("yaml")
	require.NoError(t, viper.ReadConfig(strings.NewReader(yaml)))

	cfg, err := LoadConfig()

	require.NoError(t, err)
	require.NoError(t, ValidateConfig(&cfg, ""))
	kick := cfg.Fans[0].GetKickStart()
	assert.Equal(t, KickStartFixed, kick.Mode)
	assert.Equal(t, 120, *kick.Pwm)
	assert.Equal(t, SecondsOrDuration(2500*time.Millisecond), kick.Duration)
}
