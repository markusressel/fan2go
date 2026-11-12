package configuration

import "errors"

// ConfigError marks an error caused by the configuration: the config file can't be found or read, can't be decoded,
// is invalid, or the objects described by it can't be created. Restarting fan2go doesn't help with such an error,
// so fan2go exits with a dedicated exit code for it (see cmd/exitcodes.go).
type ConfigError struct {
	Err error
}

func (e *ConfigError) Error() string {
	return e.Err.Error()
}

func (e *ConfigError) Unwrap() error {
	return e.Err
}

// NewConfigError marks err as a ConfigError. It returns nil if err is nil, and err itself if it is one already.
func NewConfigError(err error) error {
	if err == nil || IsConfigError(err) {
		return err
	}
	return &ConfigError{Err: err}
}

// IsConfigError returns whether err is, or wraps, a ConfigError
func IsConfigError(err error) bool {
	var configErr *ConfigError
	return errors.As(err, &configErr)
}
