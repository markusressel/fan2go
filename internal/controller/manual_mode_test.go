package controller

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/controller/status"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newManualModeRejectingFan returns a fan in the given control mode whose driver rejects the manual PWM mode.
func newManualModeRejectingFan(mode fans.ControlMode) *restoreTestFan {
	return &restoreTestFan{
		MockFan: MockFan{
			ID:                "fan",
			PWM:               100,
			MaxPWM:            255,
			RPM:               1000,
			ControlMode:       mode,
			PwmMap:            &configuration.PwmMapConfig{Identity: &configuration.PwmMapIdentityConfig{}},
			SetPwmToGetPwmMap: &configuration.SetPwmToGetPwmMapConfig{Identity: &configuration.SetPwmToGetPwmMapIdentityConfig{}},
		},
		features: map[fans.FeatureFlag]bool{
			fans.FeaturePwmSensor:        true,
			fans.FeatureControlModeRead:  true,
			fans.FeatureControlModeWrite: true,
		},
		failingControlModes: map[fans.ControlMode]bool{fans.ControlModePWM: true},
	}
}

func TestTrySetManualPwm_PwmModeRejected_DoesNotFallBackToDisabled(t *testing.T) {
	// GIVEN: a fan in automatic mode whose driver rejects pwm_enable=1
	fan := newManualModeRejectingFan(fans.ControlModeAutomatic)

	// WHEN
	err := trySetManualPwm(fan)

	// THEN: the error names the mode and its cause, and the fan isn't switched to disabled (full speed)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pwm (1)")
	assert.Contains(t, err.Error(), "control mode rejected")
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM}, fan.controlModeWrites)
	assert.Equal(t, fans.ControlModeAutomatic, fan.ControlMode)
}

func TestTakeManualControl_PwmModeRejected_ReturnsError(t *testing.T) {
	// GIVEN
	fan := newManualModeRejectingFan(fans.ControlModeAutomatic)

	// WHEN
	err := takeManualControl(fan)

	// THEN
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fan2go can't control the fan")
	assert.NotContains(t, fan.controlModeWrites, fans.ControlModeDisabled)
}

func TestTakeManualControl_PwmModeRejectedButFanIsInPwmModeAlready_NoError(t *testing.T) {
	// GIVEN: e.g. a read-only pwm_enable that reports manual mode already
	fan := newManualModeRejectingFan(fans.ControlModePWM)

	// WHEN
	err := takeManualControl(fan)

	// THEN
	assert.NoError(t, err)
}

func TestRunInitializationSequence_ManualModeRejected_ReturnsError(t *testing.T) {
	// GIVEN: a fan with an RPM sensor whose driver rejects the manual PWM mode
	fan := newManualModeRejectingFan(fans.ControlModeAutomatic)
	fan.features[fans.FeatureRpmSensor] = true
	controller := &DefaultFanController{fan: fan, clock: util.NewFakeClock(time.Unix(1000, 0))}
	analyzer := NewFanCurveAnalyzer(controller)

	// WHEN
	curve, err := analyzer.RunInitializationSequence(context.Background())

	// THEN: nothing is measured
	require.Error(t, err)
	assert.Nil(t, curve)
	assert.Empty(t, fan.pwmWrites)
}

func TestComputeSetPwmToGetPwmMap_ManualModeRejected_ReturnsError(t *testing.T) {
	// GIVEN: a fan without a configured set->get map whose driver rejects the manual PWM mode
	fan := newManualModeRejectingFan(fans.ControlModeAutomatic)
	fan.SetPwmToGetPwmMap = nil
	controller := &DefaultFanController{fan: fan, persistence: mockPersistence{}, clock: util.NewFakeClock(time.Unix(1000, 0))}

	// WHEN
	err := controller.computeSetPwmToGetPwmMap(context.Background())

	// THEN: the sweep doesn't run
	require.Error(t, err)
	assert.Empty(t, fan.pwmWrites)
}

func TestFanController_Run_ManualModeRejected_StopsWithErrorAndHandsTheFanBack(t *testing.T) {
	// GIVEN: a fan in automatic mode whose driver rejects pwm_enable=1, but would accept 0
	originalConfig := configuration.CurrentConfig
	defer func() {
		configuration.CurrentConfig = originalConfig
	}()
	configuration.CurrentConfig.TempSensorPollingRate = 0

	curveValue := 128.0
	clock := util.NewFakeClock(time.Unix(1000, 0))
	fan := newManualModeRejectingFan(fans.ControlModeAutomatic)
	controller := DefaultFanController{
		persistence:         mockPersistence{hasSavedPwmData: true},
		fan:                 fan,
		curve:               MockCurve{ID: "curve", Value: &curveValue},
		updateRate:          time.Hour,
		controlLoop:         control_loop.NewDirectControlLoop(nil),
		clock:               clock,
		startupSensorWarmup: defaultStartupSensorWarmup,
		startupUpdateDelay:  defaultStartupUpdateDelay,
	}

	done := make(chan error, 1)
	go func() {
		done <- controller.Run(context.Background())
	}()
	clock.BlockUntil(1)

	// WHEN: the sensor warm-up is over and the controller tries to take manual control
	clock.Advance(defaultStartupSensorWarmup)

	// THEN: Run returns the error instead of controlling the fan
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return")
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fan2go can't control the fan")

	// the fan was never switched to disabled (full speed), and is in its original mode
	assert.NotContains(t, fan.controlModeWrites, fans.ControlModeDisabled)
	assert.Equal(t, fans.ControlModeAutomatic, fan.ControlMode)
	assert.Empty(t, fan.pwmWrites)

	// the status shows that the controller doesn't control the fan
	s := controller.GetStatus()
	assert.Equal(t, status.StateStoppedError, s.State)
	assert.Contains(t, s.Error, "can't switch fan fan to control mode pwm (1)")
}

// pwmModeRejectingFan is a panickingFan whose driver rejects the manual PWM mode while rejectPwm is set.
// It records every control mode write it receives, also the rejected ones.
type pwmModeRejectingFan struct {
	*panickingFan
	mu        sync.Mutex
	rejectPwm bool
	attempts  []fans.ControlMode
}

func (fan *pwmModeRejectingFan) setRejectPwm(reject bool) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	fan.rejectPwm = reject
}

func (fan *pwmModeRejectingFan) getAttempts() []fans.ControlMode {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	return append([]fans.ControlMode{}, fan.attempts...)
}

func (fan *pwmModeRejectingFan) SetControlMode(mode fans.ControlMode) error {
	fan.mu.Lock()
	fan.attempts = append(fan.attempts, mode)
	reject := fan.rejectPwm && mode == fans.ControlModePWM
	fan.mu.Unlock()
	if reject {
		return errors.New("control mode rejected")
	}
	return fan.panickingFan.SetControlMode(mode)
}

// After onSensorFailure: restore, the recovery takes manual control again. If the driver rejects the manual
// PWM mode, the fan stays handed back (not at full speed), and the state says why.
func TestFanController_SensorFailure_Restore_RecoveryWithRejectedPwmMode_StaysHandedBack(t *testing.T) {
	// GIVEN: a fan that was handed back after a curve failure
	fan := &pwmModeRejectingFan{panickingFan: newPanickingFan()}
	test := startOriginalStateRun(t, fan, newFakePersistence(), configuration.OnSensorFailureRestore)
	test.curve.set(0, errStale)
	test.awaitNotified(1)
	mode, err := fan.GetControlMode()
	require.NoError(t, err)
	require.Equal(t, fans.ControlModeAutomatic, mode)

	// WHEN: the curve recovers, but the driver rejects the manual PWM mode now
	fan.setRejectPwm(true)
	writes := fan.getWrites()
	test.curve.set(80, nil)

	// THEN: the fan stays handed back, without a PWM write or a switch to disabled (full speed)
	require.Eventually(t, func() bool {
		test.clock.Advance(time.Millisecond)
		s := test.controller.GetStatus()
		return s.State == status.StateSensorFailureRestored && strings.Contains(s.Error, "can't switch fan fan to control mode pwm (1)")
	}, 5*time.Second, time.Millisecond)
	test.tick(3)
	assert.Equal(t, writes, fan.getWrites())
	assert.NotContains(t, fan.getAttempts(), fans.ControlModeDisabled)
	mode, err = fan.GetControlMode()
	require.NoError(t, err)
	assert.Equal(t, fans.ControlModeAutomatic, mode)

	// WHEN: the manual PWM mode can be set again
	fan.setRejectPwm(false)

	// THEN: normal control resumes
	test.awaitPwm(80)
	assertState(t, test.controller, status.StateRunning, "")
	test.stop()
	assert.NotContains(t, fan.getAttempts(), fans.ControlModeDisabled)
}
