package controller

import (
	"context"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recoveryTestTick = 200 * time.Millisecond

// newRecoveryTestController creates a controller for a fan with minPwm 0, maxPwm 200 and identity maps,
// driven by UpdateFanSpeed calls, with the given control loop and a fake clock.
func newRecoveryTestController(t *testing.T, fan *MockFan, controlLoop control_loop.ControlLoop) (*DefaultFanController, *switchableCurve, *util.FakeClock) {
	t.Cleanup(setUpRunTestConfig(time.Hour))
	curve := &switchableCurve{value: 100}
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := NewFanController(mockPersistence{hasSavedPwmData: true}, fan, curve, controlLoop, recoveryTestTick, false)
	controller.SetClock(clock)
	controller.errorNotifier = func(title, format string, a ...any) {}
	require.NoError(t, controller.computeFanSpecificMappings(context.Background()))
	return controller, curve, clock
}

func newRecoveryTestFan(onSensorFailure configuration.OnSensorFailureAction) *MockFan {
	return &MockFan{
		ID:                "fan",
		PWM:               0,
		MinPWM:            0,
		MaxPWM:            200,
		RPM:               1000,
		onSensorFailure:   onSensorFailure,
		PwmMap:            &configuration.PwmMapConfig{Identity: &configuration.PwmMapIdentityConfig{}},
		SetPwmToGetPwmMap: &configuration.SetPwmToGetPwmMapConfig{Identity: &configuration.SetPwmToGetPwmMapIdentityConfig{}},
	}
}

// updateAndTick runs UpdateFanSpeed once and advances the clock by one tick afterwards.
func updateAndTick(t *testing.T, controller *DefaultFanController, clock *util.FakeClock) {
	t.Helper()
	_ = controller.UpdateFanSpeed()
	clock.Advance(recoveryTestTick)
}

// The pid control loop is not cycled while the curve fails. After the recovery from onSensorFailure: max,
// the fan comes down from maxPwm gradually, instead of dropping to PWM 0 (the I term integrated over the
// whole failure) or jumping back to its speed before the failure.
func TestFanController_Pid_RecoveryFromMax_RampsDownFromMaxPwm(t *testing.T) {
	// GIVEN: a fan that has been running at curve value 100 with the default pid control algorithm
	fan := newRecoveryTestFan(configuration.OnSensorFailureMax)
	loop := control_loop.NewPidControlLoop(control_loop.DefaultPidConfig.P, control_loop.DefaultPidConfig.I, control_loop.DefaultPidConfig.D, recoveryTestTick)
	controller, curve, clock := newRecoveryTestController(t, fan, loop)
	for i := 0; i < 20; i++ {
		updateAndTick(t, controller, clock)
	}
	pwmBefore := fan.PWM
	require.Equal(t, 78, pwmBefore) // 0 + round(99/254 * 200)

	// WHEN: the curve fails for 60 s, and then recovers at a lower value
	curve.set(0, errStale)
	for elapsed := time.Duration(0); elapsed < time.Minute; elapsed += recoveryTestTick {
		updateAndTick(t, controller, clock)
	}
	require.Equal(t, 200, fan.PWM)
	curve.set(50, nil)
	pwms := make([]int, 0, 100)
	for i := 0; i < 100; i++ {
		updateAndTick(t, controller, clock)
		pwms = append(pwms, fan.PWM)
	}

	// THEN: the fan starts at its max and comes down smoothly to the PWM of the curve value
	pwmAfter := 39 // 0 + round(49/254 * 200)
	assert.Equal(t, 200, pwms[0], "the first update after the recovery continues from maxPwm")
	prev := 200
	for i, pwm := range pwms {
		assert.LessOrEqual(t, pwm, prev, "update %d", i)
		assert.GreaterOrEqual(t, pwm, pwmAfter, "update %d: the fan must not drop below the target (or stop)", i)
		prev = pwm
	}
	assert.Less(t, pwms[1], 200)
	assert.Greater(t, pwms[1], 190, "the second update only moves a little")
	assert.InDelta(t, pwmAfter, pwms[len(pwms)-1], 2)
}

// direct with maxPwmChangePerCycle ramps down from maxPwm after the recovery from onSensorFailure: max,
// instead of jumping back to its speed before the failure.
func TestFanController_DirectMaxChange_RecoveryFromMax_RampsDownFromMaxPwm(t *testing.T) {
	// GIVEN
	fan := newRecoveryTestFan(configuration.OnSensorFailureMax)
	maxChange := 10
	controller, curve, clock := newRecoveryTestController(t, fan, control_loop.NewDirectControlLoop(&maxChange))
	updateAndTick(t, controller, clock)
	require.Equal(t, 78, fan.PWM)

	// WHEN
	curve.set(0, errStale)
	updateAndTick(t, controller, clock)
	require.Equal(t, 200, fan.PWM)
	curve.set(100, nil)
	updateAndTick(t, controller, clock)

	// THEN: one step (10 curve units) below max: 0 + round(244/254 * 200)
	assert.Equal(t, 192, fan.PWM)
	updateAndTick(t, controller, clock)
	assert.Equal(t, 184, fan.PWM) // 0 + round(234/254 * 200)
}

func TestFanController_CalculateTargetSpeed_PresetsTheControlLoopOnRecovery(t *testing.T) {
	tests := []struct {
		name                   string
		action                 configuration.OnSensorFailureAction
		useUnscaledCurveValues bool
		expected               float64
	}{
		// a normal pid step: P 0.05*(80-100) = -1, I 100
		{name: "no failure continues from the last output", action: "", expected: 99},
		{name: "max continues from max", action: configuration.OnSensorFailureMax, expected: 255},
		{name: "max with unscaled curve values continues from maxPwm", action: configuration.OnSensorFailureMax, useUnscaledCurveValues: true, expected: 200},
		{name: "restore starts at the target", action: configuration.OnSensorFailureRestore, expected: 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN: a pid control loop that has been running at 100
			fan := newRecoveryTestFan(tt.action)
			fan.useUnscaledCurveValues = tt.useUnscaledCurveValues
			loop := control_loop.NewPidControlLoop(control_loop.DefaultPidConfig.P, control_loop.DefaultPidConfig.I, control_loop.DefaultPidConfig.D, recoveryTestTick)
			controller, curve, clock := newRecoveryTestController(t, fan, loop)
			for i := 0; i < 20; i++ {
				_, err := controller.calculateTargetSpeed()
				require.NoError(t, err)
				clock.Advance(recoveryTestTick)
			}

			// WHEN: the curve can be evaluated again (at 80) while the failure state is set
			controller.sensorFailureAction = tt.action
			curve.set(80, nil)
			target, err := controller.calculateTargetSpeed()

			// THEN
			require.NoError(t, err)
			assert.InDelta(t, tt.expected, target, 1e-9)
		})
	}
}
