package configuration

import (
	"fmt"

	"github.com/creasty/defaults"
	"github.com/spf13/viper"
)

// ReadAndValidateConfig reads the configuration file at configPath, applies defaults,
// transformations, and deprecation migrations, then validates the result.
// It returns a fully populated Configuration on success, or an error on parse or
// validation failure. CurrentConfig is never modified by this function.
func ReadAndValidateConfig(configPath string) (*Configuration, error) {
	v := viper.New()
	v.SetConfigFile(configPath)
	setDefaultValuesOnViper(v)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := &Configuration{}
	if err := defaults.Set(cfg); err != nil {
		return nil, fmt.Errorf("failed to apply struct defaults: %w", err)
	}

	if err := v.Unmarshal(cfg, makeDecodeHookOptions()); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	// Apply defaults a second time to cover nested structs created during unmarshal.
	if err := defaults.Set(cfg); err != nil {
		return nil, fmt.Errorf("failed to apply struct defaults: %w", err)
	}

	if err := applyTransformationsTo(cfg); err != nil {
		return nil, fmt.Errorf("config transformation failed: %w", err)
	}

	applyDeprecationsTo(cfg)

	if err := validateConfig(cfg, configPath); err != nil {
		return nil, err
	}

	return cfg, nil
}
