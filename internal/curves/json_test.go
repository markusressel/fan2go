package curves

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
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

// The MarshalJSON methods serialize a snapshot type, which must have the same exported fields
// (names, types, json tags, order) as the curve type, so the REST API output doesn't change.
func TestCurves_JsonSnapshotTypesMatchCurveTypes(t *testing.T) {
	assert.Equal(t, exportedFields(reflect.TypeOf(LinearSpeedCurve{})), exportedFields(reflect.TypeOf(curveJson{})))
	assert.Equal(t, exportedFields(reflect.TypeOf(PidSpeedCurve{})), exportedFields(reflect.TypeOf(curveJson{})))
	assert.Equal(t, exportedFields(reflect.TypeOf(FunctionSpeedCurve{})), exportedFields(reflect.TypeOf(curveJson{})))
	assert.Equal(t, exportedFields(reflect.TypeOf(StaircaseSpeedCurve{})), exportedFields(reflect.TypeOf(staircaseCurveJson{})))
}
