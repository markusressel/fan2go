//go:build !disable_nvml

package sensors

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNvidiaSensor_JsonSnapshotTypeMatchesSensorType(t *testing.T) {
	assert.Equal(t, exportedFields(reflect.TypeOf(NvidiaSensor{})), exportedFields(reflect.TypeOf(nvidiaSensorJson{})))
}
