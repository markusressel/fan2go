//go:build !disable_nvml

package fans

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNvidiaFan_JsonSnapshotTypeMatchesFanType(t *testing.T) {
	assert.Equal(t, exportedFields(reflect.TypeOf(NvidiaFan{})), exportedFields(reflect.TypeOf(nvidiaFanJson{})))
}
