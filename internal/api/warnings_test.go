package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/markusressel/fan2go/internal/warnings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type itemWithWarnings struct {
	Warnings []warnings.Warning `json:"warnings"`
}

// Every fan, sensor and curve has a "warnings" list, also when it has no warnings.
func TestRestApi_ItemsHaveWarnings(t *testing.T) {
	reg := createTestRegistry(t)
	reg.Warnings().Set(warnings.KindSensor, "file_sensor", "testCode", "sensor warning")
	reg.Warnings().Set(warnings.KindCurve, "pid_curve", "testCode", "curve warning")
	rest := CreateRestService(reg)

	flatRpmCurve := []warnings.Warning{{Code: warnings.CodeFlatRpmCurve, Message: "RPM stays at ~1800 across the whole PWM range."}}
	for path, expected := range map[string][]warnings.Warning{
		"/fan/hwmon_fan/":       flatRpmCurve,
		"/fan/file_fan/":        {},
		"/sensor/file_sensor/":  {{Code: "testCode", Message: "sensor warning"}},
		"/sensor/hwmon_sensor/": {},
		"/curve/pid_curve/":     {{Code: "testCode", Message: "curve warning"}},
		"/curve/linear_curve/":  {},
	} {
		t.Run(path, func(t *testing.T) {
			code, body := get(t, rest, path)
			require.Equal(t, http.StatusOK, code)
			assert.Contains(t, body, `"warnings": [`)

			var item itemWithWarnings
			require.NoError(t, json.Unmarshal([]byte(body), &item))
			assert.Equal(t, expected, item.Warnings)
		})
	}

	for path, expected := range map[string]map[string][]warnings.Warning{
		"/fan/":    {"hwmon_fan": flatRpmCurve, "file_fan": {}, "cmd_fan": {}},
		"/sensor/": {"file_sensor": {{Code: "testCode", Message: "sensor warning"}}, "hwmon_sensor": {}, "cmd_sensor": {}, "disk_sensor": {}},
	} {
		t.Run(path, func(t *testing.T) {
			code, body := get(t, rest, path)
			require.Equal(t, http.StatusOK, code)

			var items map[string]itemWithWarnings
			require.NoError(t, json.Unmarshal([]byte(body), &items))
			require.Len(t, items, len(expected))
			for id, expectedWarnings := range expected {
				assert.Equal(t, expectedWarnings, items[id].Warnings, id)
			}
		})
	}

	t.Run("/warnings/", func(t *testing.T) {
		code, body := get(t, rest, "/warnings/")
		require.Equal(t, http.StatusOK, code)

		var entries []warnings.Entry
		require.NoError(t, json.Unmarshal([]byte(body), &entries))
		assert.Equal(t, []warnings.Entry{
			{Kind: warnings.KindFan, ID: "hwmon_fan", Code: warnings.CodeFlatRpmCurve, Message: "RPM stays at ~1800 across the whole PWM range."},
			{Kind: warnings.KindSensor, ID: "file_sensor", Code: "testCode", Message: "sensor warning"},
			{Kind: warnings.KindCurve, ID: "pid_curve", Code: "testCode", Message: "curve warning"},
		}, entries)
	})
}

// Without warnings, /warnings/ returns an empty list, not null.
func TestRestApi_NoWarnings(t *testing.T) {
	reg := createTestRegistry(t)
	reg.Warnings().Clear(warnings.KindFan, "hwmon_fan", warnings.CodeFlatRpmCurve)

	code, body := get(t, CreateRestService(reg), "/warnings/")

	require.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `[]`, body)
}

func TestWithWarnings(t *testing.T) {
	store := warnings.NewStore()
	store.Set(warnings.KindFan, "fan", "code", "message")

	for name, tc := range map[string]struct {
		item     any
		id       string
		expected string
	}{
		"object":                {item: map[string]int{"a": 1}, id: "other", expected: `{"a":1,"warnings":[]}`},
		"object with warnings":  {item: map[string]int{"a": 1}, id: "fan", expected: `{"a":1,"warnings":[{"code":"code","message":"message"}]}`},
		"empty object":          {item: struct{}{}, id: "fan", expected: `{"warnings":[{"code":"code","message":"message"}]}`},
		"empty object, no warn": {item: struct{}{}, id: "other", expected: `{"warnings":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := withWarnings(tc.item, store, warnings.KindFan, tc.id)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, string(result))
		})
	}

	_, err := withWarnings([]int{1}, store, warnings.KindFan, "fan")
	assert.Error(t, err)
}
