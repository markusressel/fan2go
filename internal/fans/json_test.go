package fans

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
// (names, types, json tags, order) as the fan type, so the REST API output doesn't change.
func TestFans_JsonSnapshotTypesMatchFanTypes(t *testing.T) {
	assert.Equal(t, exportedFields(reflect.TypeOf(HwMonFan{})), exportedFields(reflect.TypeOf(hwMonFanJson{})))
	assert.Equal(t, exportedFields(reflect.TypeOf(FileFan{})), exportedFields(reflect.TypeOf(fileFanJson{})))
	assert.Equal(t, exportedFields(reflect.TypeOf(CmdFan{})), exportedFields(reflect.TypeOf(cmdFanJson{})))
}
