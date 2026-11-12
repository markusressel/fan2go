package sensors

import (
	"encoding/json"
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
		if field.Anonymous && field.Type == reflect.TypeOf(ReadingTracker{}) {
			// has no exported fields, so it adds nothing to the JSON of a sensor
			continue
		}
		if field.IsExported() {
			result = append(result, field.Name+" "+field.Type.String()+" `"+string(field.Tag)+"`")
		}
	}
	return result
}

// The MarshalJSON methods serialize a snapshot type, which must have the same exported fields
// (names, types, json tags, order) as the sensor type, so the REST API output doesn't change
// (the reading status is added by an unexported embedded struct).
func TestSensors_JsonSnapshotTypesMatchSensorTypes(t *testing.T) {
	assert.Equal(t, exportedFields(reflect.TypeOf(HwmonSensor{})), exportedFields(reflect.TypeOf(hwmonSensorJson{})))
	assert.Equal(t, exportedFields(reflect.TypeOf(FileSensor{})), exportedFields(reflect.TypeOf(fileSensorJson{})))
	assert.Equal(t, exportedFields(reflect.TypeOf(CmdSensor{})), exportedFields(reflect.TypeOf(cmdSensorJson{})))
	assert.Equal(t, exportedFields(reflect.TypeOf(DiskSensor{})), exportedFields(reflect.TypeOf(diskSensorJson{})))
}

func TestSensors_Json_ReadingStatus(t *testing.T) {
	originalConfig := configuration.CurrentConfig
	t.Cleanup(func() { configuration.CurrentConfig = originalConfig })
	configuration.CurrentConfig.SensorStaleTimeout = nil
	staleTimeout := configuration.CurrentConfig.GetSensorStaleTimeout()
	at := time.Now().Add(-time.Second).UTC().Truncate(time.Second)

	tests := []struct {
		name     string
		record   func(s Sensor)
		expected map[string]any
	}{
		{
			name:     "never read",
			record:   func(s Sensor) { s.RecordFailure() },
			expected: map[string]any{"consecutiveFailures": 1.0, "readFailures": 1.0, "stale": true},
		},
		{
			name: "fresh, after failures",
			record: func(s Sensor) {
				s.RecordFailure()
				s.RecordFailure()
				s.RecordReading(Reading{Value: 1, Time: at})
			},
			expected: map[string]any{"lastReadingTime": at.Format(time.RFC3339), "consecutiveFailures": 0.0, "readFailures": 2.0, "stale": false},
		},
		{
			name: "stale",
			record: func(s Sensor) {
				s.RecordReading(Reading{Value: 1, Time: at.Add(-staleTimeout)})
				s.RecordFailure()
			},
			expected: map[string]any{"lastReadingTime": at.Add(-staleTimeout).Format(time.RFC3339), "consecutiveFailures": 1.0, "readFailures": 1.0, "stale": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, sensor := range []Sensor{&HwmonSensor{}, &FileSensor{}, &CmdSensor{}, &DiskSensor{}} {
				tt.record(sensor)

				data, err := json.Marshal(sensor)
				require.NoError(t, err)

				var fields map[string]any
				require.NoError(t, json.Unmarshal(data, &fields))
				for _, key := range []string{"lastReadingTime", "consecutiveFailures", "readFailures", "stale"} {
					expected, ok := tt.expected[key]
					if !ok {
						assert.NotContains(t, fields, key, "%T: %s", sensor, data)
						continue
					}
					assert.Equal(t, expected, fields[key], "%T %s: %s", sensor, key, data)
				}
			}
		})
	}
}
