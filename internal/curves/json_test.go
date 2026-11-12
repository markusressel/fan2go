package curves

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exportedFields returns "Name type `tag`" of all exported fields of the given struct type, in order.
func exportedFields(t reflect.Type) []string {
	var result []string
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.IsExported() {
			result = append(result, field.Name+" "+field.Type.String()+" `"+string(field.Tag)+"`")
		}
	}
	return result
}

// evaluationFields are the fields the snapshot types add to the fields of the curve types
// (after Config and Value): the error and time of the last evaluation.
var evaluationFields = []string{
	"Error string `json:\"error,omitempty\"`",
	"EvaluatedAt time.Time `json:\"evaluatedAt,omitzero\"`",
}

// withEvaluationFields inserts evaluationFields into the exported fields of a curve type, after Config and Value.
func withEvaluationFields(fields []string) []string {
	result := append([]string{}, fields[:2]...)
	result = append(result, evaluationFields...)
	return append(result, fields[2:]...)
}

// The MarshalJSON methods serialize a snapshot type, which must have the same exported fields
// (names, types, json tags, order) as the curve type, so the existing REST API output doesn't change,
// plus the result of the last evaluation (omitted until the curve has been evaluated).
func TestCurves_JsonSnapshotTypesMatchCurveTypes(t *testing.T) {
	assert.Equal(t, withEvaluationFields(exportedFields(reflect.TypeOf(LinearSpeedCurve{}))), exportedFields(reflect.TypeOf(curveJson{})))
	assert.Equal(t, withEvaluationFields(exportedFields(reflect.TypeOf(PidSpeedCurve{}))), exportedFields(reflect.TypeOf(curveJson{})))
	assert.Equal(t, withEvaluationFields(exportedFields(reflect.TypeOf(FunctionSpeedCurve{}))), exportedFields(reflect.TypeOf(curveJson{})))
	// except LastTemp: the snapshot has the json name lastTemp, and omits math.MinInt (no step)
	staircaseFields := exportedFields(reflect.TypeOf(StaircaseSpeedCurve{}))
	assert.Equal(t, "LastTemp int ``", staircaseFields[len(staircaseFields)-1])
	expectedStaircaseFields := append(withEvaluationFields(staircaseFields[:len(staircaseFields)-1]), "LastTemp *int `json:\"lastTemp,omitempty\"`")
	assert.Equal(t, expectedStaircaseFields, exportedFields(reflect.TypeOf(staircaseCurveJson{})))
}

func TestStaircaseCurve_Json_LastTemp(t *testing.T) {
	steps := map[int]float64{40: 100, 60: 200}
	tests := []struct {
		name     string
		lastTemp int
		expected any
	}{
		{name: "no step (before the first evaluation, or below the lowest step)", lastTemp: math.MinInt, expected: nil},
		{name: "step", lastTemp: 40, expected: 40.0},
		{name: "step 0 is not omitted", lastTemp: 0, expected: 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			curve := &StaircaseSpeedCurve{
				Config:   configuration.CurveConfig{ID: "staircase", Staircase: &configuration.StaircaseCurveConfig{Sensor: "s", Steps: steps}},
				LastTemp: tt.lastTemp,
			}

			data, err := json.Marshal(curve)
			require.NoError(t, err)

			var fields map[string]any
			require.NoError(t, json.Unmarshal(data, &fields))
			assert.NotContains(t, fields, "LastTemp")
			value, ok := fields["lastTemp"]
			if tt.expected == nil {
				assert.False(t, ok, "lastTemp must be omitted: %s", data)
			} else {
				assert.Equal(t, tt.expected, value)
			}
		})
	}
}

// The REST API shows the result of the last evaluation of a curve: its value, error and time.
func TestCurves_Json_ContainsResultOfLastEvaluation(t *testing.T) {
	curve := &LinearSpeedCurve{}
	data, err := json.Marshal(curve)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "evaluatedAt", "not evaluated yet")
	assert.NotContains(t, string(data), "error")

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	_, _ = curve.storeResult(42, nil, at)
	data, err = json.Marshal(curve)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"value":42,"evaluatedAt":"2026-01-02T03:04:05Z"`)
	assert.NotContains(t, string(data), "error")

	_, _ = curve.storeResult(0, errors.New("sensor is stale"), at.Add(time.Second))
	data, err = json.Marshal(curve)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"value":42,"error":"sensor is stale","evaluatedAt":"2026-01-02T03:04:06Z"`)
}
