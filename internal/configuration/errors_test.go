package configuration

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewConfigError(t *testing.T) {
	cause := errors.New("invalid value")

	err := NewConfigError(cause)

	assert.True(t, IsConfigError(err))
	assert.True(t, IsConfigError(fmt.Errorf("wrapped: %w", err)))
	assert.ErrorIs(t, err, cause)
	assert.EqualError(t, err, "invalid value")
	assert.Same(t, err, NewConfigError(err), "an error is marked only once")
	assert.NoError(t, NewConfigError(nil))
	assert.False(t, IsConfigError(cause))
	assert.False(t, IsConfigError(nil))
}

func TestValidateConfig_ReturnsConfigError(t *testing.T) {
	// GIVEN a fan that references a curve that doesn't exist
	cfg := Configuration{
		Fans: []FanConfig{{ID: "fan", Curve: "missing", File: &FileFanConfig{Path: "/tmp/fan"}}},
	}

	err := ValidateConfig(&cfg, "")

	assert.Error(t, err)
	assert.True(t, IsConfigError(err))
}
