package sensors

import (
	"os/exec"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
)

func TestCmdSensor_GetValue(t *testing.T) {
	// GIVEN
	echo, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("echo not found")
	}
	sensor := &CmdSensor{
		Config: configuration.SensorConfig{
			ID: "cmd",
			Cmd: &configuration.CmdSensorConfig{
				Exec: echo,
				Args: []string{"42000"},
			},
		},
	}

	// WHEN
	value, err := sensor.GetValue()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 42000.0, value)
}

func TestCmdSensor_GetValue_Timeout(t *testing.T) {
	// GIVEN
	sensor := &CmdSensor{
		Config: configuration.SensorConfig{
			ID: "cmd",
			Cmd: &configuration.CmdSensorConfig{
				Exec:    "/usr/bin/sleep",
				Args:    []string{"5"},
				Timeout: 50 * time.Millisecond,
			},
		},
	}

	// WHEN
	start := time.Now()
	_, err := sensor.GetValue()

	// THEN: the configured timeout is used instead of the 2 s default
	assert.Error(t, err)
	assert.Less(t, time.Since(start), 1*time.Second)
}
