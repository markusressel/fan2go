package controller

import (
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
)

func TestMeasureRpm_DoesNotMutatePersistedCurveData(t *testing.T) {
	originalConfig := configuration.CurrentConfig
	defer func() {
		configuration.CurrentConfig = originalConfig
	}()

	configuration.CurrentConfig.RpmRollingWindowSize = 10

	curve := map[int]float64{100: 777}
	fan := &MockFan{
		ID:         "fan",
		PWM:        100,
		RPM:        1234,
		speedCurve: &curve,
	}

	controller := &DefaultFanController{fan: fan}
	controller.measureRpm(fan)

	assert.Equal(t, map[int]float64{100: 777.0}, curve)
}
