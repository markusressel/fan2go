package fans

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestFileFan_GetId(t *testing.T) {
	// GIVEN
	id := "test"
	config := configuration.FanConfig{
		ID: id,
		File: &configuration.FileFanConfig{
			Path:    "/path/to/pwm",
			RpmPath: "/path/to/rpm",
		},
	}
	fan, err := NewFan(config)
	assert.NoError(t, err)

	// WHEN
	result := fan.GetId()

	assert.Equal(t, id, result)
}

func TestFileFan_GetStartPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "/path/to/pwm",
			RpmPath: "/path/to/rpm",
		},
	}
	fan, err := NewFan(config)
	assert.NoError(t, err)

	// WHEN
	result := fan.GetStartPwm()

	// THEN
	assert.Equal(t, 1, result)
}

func TestFileFan_SetStartPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "/path/to/pwm",
			RpmPath: "/path/to/rpm",
		},
	}

	fan, err := NewFan(config)
	assert.NoError(t, err)

	// WHEN
	fan.SetStartPwm(100, false)

	// THEN
	// NOTE: file fan does not support setting start pwm
	assert.Equal(t, 1, fan.GetStartPwm())
}

func TestFileFan_GetMinPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "/path/to/pwm",
			RpmPath: "/path/to/rpm",
		},
	}

	fan, err := NewFan(config)
	assert.NoError(t, err)

	// WHEN
	result := fan.GetMinPwm()

	// THEN
	assert.Equal(t, 0, result)
}

func TestFileFan_SetMinPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "/path/to/pwm",
			RpmPath: "/path/to/rpm",
		},
	}

	fan, err := NewFan(config)
	assert.NoError(t, err)

	// WHEN
	fan.SetMinPwm(100, false)

	// THEN
	// NOTE: file fan does not support setting start pwm
	assert.Equal(t, 0, fan.GetMinPwm())
}

func TestFileFan_GetMaxPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "/path/to/pwm",
			RpmPath: "/path/to/rpm",
		},
	}

	fan, err := NewFan(config)
	assert.NoError(t, err)

	// WHEN
	result := fan.GetMaxPwm()

	// THEN
	assert.Equal(t, 255, result)
}

func TestFileFan_SetMaxPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		File: &configuration.FileFanConfig{
			Path:    "/path/to/pwm",
			RpmPath: "/path/to/rpm",
		},
	}

	fan, err := NewFan(config)
	assert.NoError(t, err)

	// WHEN
	fan.SetMaxPwm(100, false)

	// THEN
	// NOTE: file fan does not support setting max pwm
	assert.Equal(t, 255, fan.GetMaxPwm())
}
