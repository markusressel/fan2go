package sensors

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
// (names, types, json tags, order) as the sensor type, so the REST API output doesn't change.
func TestSensors_JsonSnapshotTypesMatchSensorTypes(t *testing.T) {
	assert.Equal(t, exportedFields(reflect.TypeOf(HwmonSensor{})), exportedFields(reflect.TypeOf(hwmonSensorJson{})))
	assert.Equal(t, exportedFields(reflect.TypeOf(FileSensor{})), exportedFields(reflect.TypeOf(fileSensorJson{})))
	assert.Equal(t, exportedFields(reflect.TypeOf(CmdSensor{})), exportedFields(reflect.TypeOf(cmdSensorJson{})))
	assert.Equal(t, exportedFields(reflect.TypeOf(DiskSensor{})), exportedFields(reflect.TypeOf(diskSensorJson{})))
}
