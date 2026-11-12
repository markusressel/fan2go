package configuration

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

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

// IsSet returns whether the value was set in the configuration or overridden at runtime,
// i.e. whether Get returns something other than the default.
func (b *DefaultTrueBool) IsSet() bool {
	return b.Present || b.RuntimeOverride
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

// durationMillisecondsHookFunc allows numeric duration values in configuration.
// Integer and float values are interpreted as milliseconds for backwards compatibility
// with configs that use plain numbers (e.g. 200 => 200ms).
func durationMillisecondsHookFunc() mapstructure.DecodeHookFuncType {
	durationType := reflect.TypeOf(time.Duration(0))

	return func(
		f reflect.Type,
		t reflect.Type,
		data interface{},
	) (interface{}, error) {
		if t != durationType {
			return data, nil
		}

		switch v := data.(type) {
		case int:
			return time.Duration(v) * time.Millisecond, nil
		case int8:
			return time.Duration(v) * time.Millisecond, nil
		case int16:
			return time.Duration(v) * time.Millisecond, nil
		case int32:
			return time.Duration(v) * time.Millisecond, nil
		case int64:
			return time.Duration(v) * time.Millisecond, nil
		case uint:
			return time.Duration(v) * time.Millisecond, nil
		case uint8:
			return time.Duration(v) * time.Millisecond, nil
		case uint16:
			return time.Duration(v) * time.Millisecond, nil
		case uint32:
			return time.Duration(v) * time.Millisecond, nil
		case uint64:
			return time.Duration(v) * time.Millisecond, nil
		case float32:
			return time.Duration(float64(v) * float64(time.Millisecond)), nil
		case float64:
			return time.Duration(v * float64(time.Millisecond)), nil
		default:
			return data, nil
		}
	}
}

// SecondsOrDuration is a duration that is configured either as a plain number of seconds
// (e.g. `2`, `0.5`, or `"2"` from the command line) or as a Go duration string (e.g. `"500ms"`, `"2s"`).
// Unlike plain time.Duration fields, where durationMillisecondsHookFunc reads a plain number as
// milliseconds, a plain number means seconds here. This keeps fields that used to be an integer
// number of seconds backwards compatible.
type SecondsOrDuration time.Duration

// Duration returns the value as a time.Duration.
func (d SecondsOrDuration) Duration() time.Duration {
	return time.Duration(d)
}

func (d SecondsOrDuration) String() string {
	return time.Duration(d).String()
}

// secondsOrDurationHookFunc decodes numbers (seconds) and strings (a plain number of seconds
// or a Go duration) into a SecondsOrDuration.
func secondsOrDurationHookFunc() mapstructure.DecodeHookFuncType {
	targetType := reflect.TypeOf(SecondsOrDuration(0))

	return func(
		f reflect.Type,
		t reflect.Type,
		data interface{},
	) (interface{}, error) {
		if t != targetType {
			return data, nil
		}

		switch v := data.(type) {
		case SecondsOrDuration:
			return v, nil
		case time.Duration:
			return SecondsOrDuration(v), nil
		case string:
			if seconds, err := strconv.ParseFloat(v, 64); err == nil {
				return secondsToDuration(seconds)
			}
			parsed, err := time.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("cannot parse %q as a number of seconds or a duration (e.g. \"500ms\"): %w", v, err)
			}
			return SecondsOrDuration(parsed), nil
		}

		value := reflect.ValueOf(data)
		switch value.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return secondsToDuration(float64(value.Int()))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return secondsToDuration(float64(value.Uint()))
		case reflect.Float32, reflect.Float64:
			return secondsToDuration(value.Float())
		default:
			return data, nil
		}
	}
}

// maxSeconds is the largest number of seconds a time.Duration can hold.
var maxSeconds = float64(math.MaxInt64) / float64(time.Second)

func secondsToDuration(seconds float64) (SecondsOrDuration, error) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || math.Abs(seconds) > maxSeconds {
		return 0, fmt.Errorf("%v is not a valid number of seconds", seconds)
	}
	return SecondsOrDuration(time.Duration(seconds * float64(time.Second))), nil
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

// CurveUnits is a difference of curve values (e.g. a deadband), or a rate of curve values per second.
// It is configured as a plain number of curve units (the curve value range is [0..255]),
// or as a percentage of that range (e.g. "10%"), which is converted linearly: 1% is 2.55.
// Unlike the absolute values of curve steps, where 1% is 1 (the minimum speed), a percentage
// of a difference or rate has no offset.
type CurveUnits float64

// curveUnitsPerPercent converts a percentage of the curve value range into curve units
const curveUnitsPerPercent = 255.0 / 100.0

// Value returns the value in curve units.
func (c CurveUnits) Value() float64 {
	return float64(c)
}

// UnmarshalText parses a plain number (curve units) or a percentage ("10%").
func (c *CurveUnits) UnmarshalText(text []byte) error {
	value, err := parseCurveUnits(string(text))
	if err != nil {
		return err
	}
	*c = value
	return nil
}

// UnmarshalJSON accepts a number (curve units) or a string (see UnmarshalText).
func (c *CurveUnits) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch v := value.(type) {
	case float64:
		*c = CurveUnits(v)
		return nil
	case string:
		return c.UnmarshalText([]byte(v))
	default:
		return fmt.Errorf("cannot parse %s as a number or a percentage (e.g. \"10%%\")", string(data))
	}
}

func parseCurveUnits(text string) (CurveUnits, error) {
	str := strings.TrimSpace(text)
	percent := strings.HasSuffix(str, "%")
	number := strings.TrimSpace(strings.TrimSuffix(str, "%"))
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("cannot parse %q as a number or a percentage (e.g. \"10%%\")", text)
	}
	if percent {
		value *= curveUnitsPerPercent
	}
	return CurveUnits(value), nil
}
