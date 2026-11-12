package controller

import (
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDefaultControlLoop creates the control loop of a fan without controlAlgorithm (see createControlLoop).
func newDefaultControlLoop() *control_loop.DirectControlLoop {
	direct := configuration.DefaultControlAlgorithmConfig().Direct
	return control_loop.NewDirectControlLoopWithOptions(control_loop.DirectControlLoopOptions{
		TimeConstant: direct.TimeConstant.Duration(),
		Deadband:     direct.Deadband.Value(),
	})
}

// After the recovery from onSensorFailure: max, the default control algorithm brings the fan down from
// maxPwm with its time constant, instead of dropping it to the curve value at once.
func TestFanController_DefaultControlAlgorithm_RecoveryFromMax_ComesDownFromMaxPwm(t *testing.T) {
	// GIVEN: a fan that has been running at curve value 100
	fan := newRecoveryTestFan(configuration.OnSensorFailureMax)
	controller, curve, clock := newRecoveryTestController(t, fan, newDefaultControlLoop())
	for i := 0; i < 20; i++ {
		updateAndTick(t, controller, clock)
	}
	require.Equal(t, 78, fan.PWM) // 0 + round(99/254 * 200)

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
	// one tick of the low-pass: 255 - 205 x (1 - e^(-0.2/2)) = 235.5 => 0 + round(234.5/254 * 200)
	assert.Equal(t, 185, pwms[1])
	prev := 200
	for i, pwm := range pwms {
		assert.LessOrEqual(t, pwm, prev, "update %d", i)
		assert.GreaterOrEqual(t, pwm, pwmAfter, "update %d: the fan must not drop below the target (or stop)", i)
		prev = pwm
	}
	// after 2 s (one time constant): 50 + 205 x e^-1 = 125.4 (+- the deadband)
	assert.InDelta(t, 98, pwms[10], 3)
	assert.InDelta(t, pwmAfter, pwms[len(pwms)-1], 2)
}

func TestFanController_DefaultControlAlgorithm_CalculateTargetSpeedOnRecovery(t *testing.T) {
	tests := []struct {
		name     string
		action   configuration.OnSensorFailureAction
		expected float64
	}{
		// one tick of the low-pass: 100 - 20 x (1 - e^-0.1) = 98.1, held by the deadband
		{name: "no failure continues from the last output", action: "", expected: 100},
		{name: "max continues from max", action: configuration.OnSensorFailureMax, expected: 255},
		{name: "restore starts at the target", action: configuration.OnSensorFailureRestore, expected: 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN: the default control loop has been running at 100
			fan := newRecoveryTestFan(tt.action)
			controller, curve, clock := newRecoveryTestController(t, fan, newDefaultControlLoop())
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

// The default control algorithm (low-pass and deadband) keeps the PWM value steady while the curve value jitters
// a little, so the neverStop stall detection (which only acts on a steady PWM value) still detects a stalled fan.
func TestFanController_DefaultControlAlgorithm_NeverStop_StallDetectionWorksWithAJitteringCurve(t *testing.T) {
	// GIVEN: a running fan with the default control algorithm, at a curve value that jitters between 10 and 12.5
	setupStallTestConfig(t)
	s := newStallTestHwMonFan(t, 20, 255)
	controller, clock := newStallTestController(t, s.fan)
	controller.controlLoop = newDefaultControlLoop()
	curveValue := 10.0
	controller.curve = MockCurve{ID: "curve", Value: &curveValue}
	update := func(n int) {
		for i := 0; i < n; i++ {
			if curveValue == 10 {
				curveValue = 12.5
			} else {
				curveValue = 10
			}
			require.NoError(t, pollAndUpdate(t, controller, clock))
		}
	}
	s.setRpm(t, "1000")
	update(20)
	pwm := s.pwm(t)
	assert.Equal(t, 31, pwm) // the first curve value is 12.5: 20 + round(11.5/254 * 235)

	// WHEN the fan stops
	s.setRpm(t, "0")
	// 1000 * 0.9^28 = 52.3 RPM: still above the spin threshold (50 RPM)
	update(28)
	assert.Equal(t, 0, controller.GetStatistics().IncreasedMinPwmCount)
	assert.Equal(t, pwm, s.pwm(t))
	// 1000 * 0.9^29 = 47.1 RPM: below the spin threshold
	update(1)

	// THEN: the stall is detected (the floor minPwm + offset is below the PWM value of the curve, so the
	// PWM value doesn't change yet)
	assert.Equal(t, 1, controller.GetStatistics().IncreasedMinPwmCount)
}

// Run resets the runtime state of the controller (resetRuntimeState), including the control loop: after a
// restart, the default control algorithm starts at the curve value instead of smoothing from its old state.
func TestFanController_DefaultControlAlgorithm_ResetRuntimeStateStartsAtTheCurveValue(t *testing.T) {
	// GIVEN: the loop was preset at max (e.g. during a sensor failure before the restart)
	fan := newRecoveryTestFan(configuration.OnSensorFailureMax)
	controller, curve, clock := newRecoveryTestController(t, fan, newDefaultControlLoop())
	_, err := controller.calculateTargetSpeed()
	require.NoError(t, err)
	controller.controlLoop.Preset(255)

	// WHEN
	controller.resetRuntimeState()
	clock.Advance(recoveryTestTick)
	curve.set(80, nil)
	target, err := controller.calculateTargetSpeed()

	// THEN
	require.NoError(t, err)
	assert.Equal(t, 80.0, target)
}
