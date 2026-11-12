package controller

import (
	"context"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLastSetPwmTestController(t *testing.T, fan *MockFan) *DefaultFanController {
	t.Helper()
	controller := &DefaultFanController{
		persistence: mockPersistence{hasPwmMap: false},
		fan:         fan,
		updateRate:  time.Duration(100),
	}
	require.NoError(t, controller.computeFanSpecificMappings(context.Background()))
	return controller
}

func TestFanController_GetLastSetPwm_UnknownUntilSet(t *testing.T) {
	controller := newLastSetPwmTestController(t, &MockFan{ID: "fan", speedCurve: &LinearFan})

	_, ok := controller.GetLastSetPwm()

	assert.False(t, ok)
}

func TestFanController_GetLastSetPwm_IsRawValueAfterPwmMap(t *testing.T) {
	// GIVEN a fan with a pwmMap that maps target 100 to raw value 39
	fan := &MockFan{
		ID:         "fan",
		speedCurve: &LinearFan,
		PwmMap: func() *configuration.PwmMapConfig {
			pts := configuration.PwmMapValuesConfig(PwmMapForFanWithLimitedRange)
			return &configuration.PwmMapConfig{Values: &pts}
		}(),
	}
	controller := newLastSetPwmTestController(t, fan)

	// WHEN
	require.NoError(t, controller.setTargetPwm(100))

	// THEN
	pwm, ok := controller.GetLastSetPwm()
	assert.True(t, ok)
	assert.Equal(t, 39, pwm)
	assert.Equal(t, 39, fan.PWM)
}

func TestFanController_GetLastSetPwm_FanAlreadyAtTarget(t *testing.T) {
	// GIVEN a fan that already runs at the target, so setTargetPwm doesn't write it again
	fan := &MockFan{ID: "fan", PWM: 0, speedCurve: &LinearFan}
	controller := newLastSetPwmTestController(t, fan)
	fan.PWM = 100

	// WHEN
	require.NoError(t, controller.setTargetPwm(100))

	// THEN
	pwm, ok := controller.GetLastSetPwm()
	assert.True(t, ok)
	assert.Equal(t, 100, pwm)
}

func TestFanController_GetRpmAvg_UnknownUntilSeeded(t *testing.T) {
	// GIVEN
	fan := &MockFan{ID: "fan", RPM: 1200, speedCurve: &LinearFan}
	controller := newLastSetPwmTestController(t, fan)

	// THEN the average is unknown before the RPM monitor has measured the fan
	_, ok := controller.GetRpmAvg()
	assert.False(t, ok)

	// WHEN the RPM monitor measures the fan
	controller.measureRpm(fan)

	// THEN
	rpm, ok := controller.GetRpmAvg()
	assert.True(t, ok)
	assert.Equal(t, 1200.0, rpm)
}

func TestFanController_GetLastSetPwm_NotUpdatedWhenWriteFails(t *testing.T) {
	// GIVEN a fan that was set to 100 successfully
	fan := &MockFan{ID: "fan", speedCurve: &LinearFan}
	controller := newLastSetPwmTestController(t, fan)
	require.NoError(t, controller.setTargetPwm(100))

	// WHEN writing the next value fails
	fan.setPwmAlwaysFails = true
	err := controller.setTargetPwm(200)

	// THEN the last successfully set value is kept
	assert.Error(t, err)
	pwm, ok := controller.GetLastSetPwm()
	assert.True(t, ok)
	assert.Equal(t, 100, pwm)
}
