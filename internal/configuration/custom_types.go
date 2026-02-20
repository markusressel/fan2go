package configuration

import (
	"reflect"
	"strconv"

	"github.com/go-viper/mapstructure/v2"
)

// Optional is a generic container for optional configuration values.
type Optional[T any] struct {
	// Value holds the actual as unmarshalled.
	Value T
	// Present indicates if the value was present in the configuration.
	Present bool
	// RuntimeOverride indicates if the value was overridden at runtime.
	RuntimeOverride bool
}

// Get returns the value if present or overridden, otherwise it returns the provided defaultValue.
func (o *Optional[T]) Get() T {
	return o.Value
}

// SetOverride sets the value and marks it as overridden at runtime.
func (o *Optional[T]) SetOverride(value T) {
	o.RuntimeOverride = true
	o.Value = value
}

// DefaultTrueBool is a boolean type that defaults to true if not present and not overridden.
type DefaultTrueBool struct {
	Optional[bool]
}

// Get returns the boolean value, defaulting to true if not present and not overridden.
func (b *DefaultTrueBool) Get() bool {
	if !b.Present && !b.RuntimeOverride {
		return true
	}
	return b.Value
}

// DefaultTrueBoolHookFunc returns a mapstructure decode hook function for DefaultTrueBool.
func DefaultTrueBoolHookFunc() mapstructure.DecodeHookFuncType {
	return func(
		f reflect.Type,
		t reflect.Type,
		data interface{}) (interface{}, error) {

		// Only target our specific named type
		if t != reflect.TypeOf(DefaultTrueBool{}) {
			return data, nil
		}

		var val bool
		switch v := data.(type) {
		case bool:
			val = v
		case string:
			parsed, err := strconv.ParseBool(v)
			if err != nil {
				return data, nil
			}
			val = parsed
		default:
			return data, nil
		}

		// Return the specific type with the inner Optional initialized
		return DefaultTrueBool{
			Optional: Optional[bool]{
				Value:   val,
				Present: true,
			},
		}, nil
	}
}
