package configuration

import (
	"fmt"
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

// pwmMapPointsHookFunc returns a mapstructure decode hook that handles:
//  1. Key-type conversion for PwmMapLinearConfig and PwmMapValuesConfig
//     (interface{} keys from YAML → int).
//  2. Legacy bare-map format for PwmMapConfig: a numeric-keyed map with no
//     "linear"/"values"/"autodetect"/"identity" string keys is treated as the
//     old "values" format and decoded into PwmMapConfig{Values: &pts}.
func pwmMapPointsHookFunc() mapstructure.DecodeHookFuncType {
	linearType := reflect.TypeOf(PwmMapLinearConfig{})
	valuesType := reflect.TypeOf(PwmMapValuesConfig{})
	pwmMapType := reflect.TypeOf(PwmMapConfig{})
	setPwmLinearType := reflect.TypeOf(SetPwmToGetPwmMapLinearConfig{})
	setPwmValuesType := reflect.TypeOf(SetPwmToGetPwmMapValuesConfig{})
	controlModeValueType := reflect.TypeOf(ControlModeValue(""))

	return func(
		f reflect.Type,
		t reflect.Type,
		data interface{},
	) (interface{}, error) {
		// ControlModeValue: allow integer YAML values (e.g. active: 1) to decode as string
		if t == controlModeValueType {
			switch v := data.(type) {
			case int:
				return ControlModeValue(strconv.Itoa(v)), nil
			case string:
				return ControlModeValue(v), nil
			}
		}

		// 3a — key-type conversion for the named map types
		if t == linearType || t == valuesType {
			pts, err := parsePwmIntMap(data)
			if err != nil {
				return nil, err
			}
			if t == linearType {
				cfg := PwmMapLinearConfig(pts)
				return cfg, nil
			}
			cfg := PwmMapValuesConfig(pts)
			return cfg, nil
		}

		if t == setPwmLinearType || t == setPwmValuesType {
			pts, err := parsePwmIntMap(data)
			if err != nil {
				return nil, err
			}
			if t == setPwmLinearType {
				cfg := SetPwmToGetPwmMapLinearConfig(pts)
				return cfg, nil
			}
			cfg := SetPwmToGetPwmMapValuesConfig(pts)
			return cfg, nil
		}

		// 3b — legacy bare-map backwards compat for PwmMapConfig
		if t == pwmMapType {
			if isBarePwmMap(data) {
				pts, err := parsePwmIntMap(data)
				if err != nil {
					return nil, fmt.Errorf("pwmMap (legacy format): %w", err)
				}
				cfg := PwmMapValuesConfig(pts)
				return PwmMapConfig{Values: &cfg}, nil
			}
		}

		return data, nil
	}
}

// isBarePwmMap returns true when data is a map whose keys are all numeric
// (no "linear", "values", "autodetect", or "identity" string keys).
func isBarePwmMap(data interface{}) bool {
	modeKeys := map[string]bool{"linear": true, "values": true, "autodetect": true, "identity": true}
	switch v := data.(type) {
	case map[string]interface{}:
		for k := range v {
			if modeKeys[k] {
				return false
			}
		}
		return len(v) > 0
	case map[interface{}]interface{}:
		for k := range v {
			if ks, ok := k.(string); ok && modeKeys[ks] {
				return false
			}
		}
		return len(v) > 0
	}
	return false
}

// parsePwmIntMap converts various map types (from YAML decoding) into map[int]int.
func parsePwmIntMap(data interface{}) (map[int]int, error) {
	result := make(map[int]int)
	switch v := data.(type) {
	case map[interface{}]interface{}:
		for k, val := range v {
			key, err := anyToInt(k)
			if err != nil {
				return nil, fmt.Errorf("invalid key %v: %w", k, err)
			}
			value, err := anyToInt(val)
			if err != nil {
				return nil, fmt.Errorf("invalid value %v: %w", val, err)
			}
			result[key] = value
		}
	case map[string]interface{}:
		for k, val := range v {
			key, err := anyToInt(k)
			if err != nil {
				return nil, fmt.Errorf("invalid key %q: %w", k, err)
			}
			value, err := anyToInt(val)
			if err != nil {
				return nil, fmt.Errorf("invalid value %v: %w", val, err)
			}
			result[key] = value
		}
	case map[int]int:
		return v, nil
	default:
		return nil, fmt.Errorf("unsupported point map type %T", data)
	}
	return result, nil
}

// anyToInt converts numeric and string values to int.
func anyToInt(v interface{}) (int, error) {
	switch val := v.(type) {
	case int:
		return val, nil
	case int64:
		return int(val), nil
	case float64:
		return int(val), nil
	case string:
		n, err := strconv.Atoi(val)
		if err != nil {
			return 0, fmt.Errorf("cannot parse %q as int: %w", val, err)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("cannot convert %T to int", v)
	}
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
