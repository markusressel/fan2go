package controller

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/controller/status"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPwmWriteFailureTestController returns a controller for a MockFan (identity pwmMap, unscaled curve values)
// whose curve value is *curveValue, with a fake clock.
func newPwmWriteFailureTestController(fan *MockFan, curveValue *float64) (*DefaultFanController, *util.FakeClock) {
	fan.useUnscaledCurveValues = true
	controller := newIdentityMappedController(fan, 0)
	controller.curve = MockCurve{ID: "curve", Value: curveValue}
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller.SetClock(clock)
	return controller, clock
}

func setPwmFails(fan *MockFan, fails bool) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	fan.setPwmAlwaysFails = fails
}

func TestFanController_SetTargetPwm_FailedWrite_KeepsLastTarget(t *testing.T) {
	// GIVEN a fan that was set to 100 successfully
	fan := &MockFan{ID: "fan"}
	curveValue := 100.0
	controller, _ := newPwmWriteFailureTestController(fan, &curveValue)
	require.NoError(t, controller.setTargetPwm(100))

	// WHEN writing the next value fails
	setPwmFails(fan, true)
	err := controller.setTargetPwm(200)

	// THEN the last target is still the value that was actually written
	assert.Error(t, err)
	require.NotNil(t, controller.lastTarget)
	assert.Equal(t, 100, *controller.lastTarget)
	lastSetPwm, ok := controller.GetLastSetPwm()
	assert.True(t, ok)
	assert.Equal(t, 100, lastSetPwm)
}

func TestFanController_UpdateFanSpeed_FailedWrite_IsNotCountedAsThirdPartyChange(t *testing.T) {
	// GIVEN a fan that was set to 100 by the controller
	fan := &MockFan{ID: "fan"}
	curveValue := 100.0
	controller, clock := newPwmWriteFailureTestController(fan, &curveValue)
	require.NoError(t, controller.UpdateFanSpeed())
	require.Equal(t, 100, fan.PWM)

	// WHEN the curve changes, but writing the new value fails
	setPwmFails(fan, true)
	curveValue = 200
	for i := 0; i < 3; i++ {
		clock.Advance(time.Second)
		require.NoError(t, controller.UpdateFanSpeed())
	}

	// THEN the fan still runs at the value fan2go set, which is not a change by a third party
	assert.Equal(t, 100, fan.PWM)
	assert.Equal(t, 0, controller.GetStatistics().UnexpectedPwmValueCount)
}

func TestFanController_UpdateFanSpeed_ShortWriteFailureBurst_Recovers(t *testing.T) {
	// GIVEN
	fan := &MockFan{ID: "fan"}
	curveValue := 100.0
	controller, clock := newPwmWriteFailureTestController(fan, &curveValue)
	require.NoError(t, controller.UpdateFanSpeed())

	for round := 0; round < 2; round++ {
		// WHEN writes fail for a little less than pwmWriteFailureTimeout
		setPwmFails(fan, true)
		curveValue = float64(150 + round)
		for elapsed := time.Duration(0); elapsed < pwmWriteFailureTimeout; elapsed += time.Second {
			// THEN the controller keeps trying
			require.NoError(t, controller.UpdateFanSpeed(), "round %d, after %s", round, elapsed)
			clock.Advance(time.Second - time.Millisecond)
		}

		// WHEN writing works again
		setPwmFails(fan, false)

		// THEN the new value is applied, and the next failure starts a new time window
		require.NoError(t, controller.UpdateFanSpeed())
		assert.Equal(t, 150+round, fan.PWM)
		assert.Equal(t, 0, controller.pwmWriteFailures.count)
		clock.Advance(time.Second)
	}
}

func TestFanController_UpdateFanSpeed_PersistentWriteFailure_ReturnsError(t *testing.T) {
	// GIVEN a fan whose PWM can't be written
	fan := &MockFan{ID: "fan", setPwmAlwaysFails: true}
	curveValue := 100.0
	controller, clock := newPwmWriteFailureTestController(fan, &curveValue)

	// WHEN / THEN: the controller keeps trying for pwmWriteFailureTimeout
	for elapsed := time.Duration(0); elapsed < pwmWriteFailureTimeout; elapsed += 200 * time.Millisecond {
		require.NoError(t, controller.UpdateFanSpeed(), "after %s", elapsed)
		clock.Advance(200 * time.Millisecond)
	}

	// and then gives up
	err := controller.UpdateFanSpeed()
	assert.ErrorIs(t, err, ErrPwmWriteFailed)
	assert.ErrorContains(t, err, "write rejected by hardware")
	assert.ErrorContains(t, err, "reload")
}

func TestFanController_UpdateFanSpeed_SlowWriteFailures_NeedMinimumAttempts(t *testing.T) {
	// GIVEN a fan whose PWM writes fail, and take longer than pwmWriteFailureTimeout each
	fan := &MockFan{ID: "fan", setPwmAlwaysFails: true}
	curveValue := 100.0
	controller, clock := newPwmWriteFailureTestController(fan, &curveValue)

	// WHEN / THEN: the controller gives up only after pwmWriteFailureMinAttempts attempts
	for i := 1; i < pwmWriteFailureMinAttempts; i++ {
		require.NoError(t, controller.UpdateFanSpeed(), "attempt %d", i)
		clock.Advance(2 * pwmWriteFailureTimeout)
	}
	assert.ErrorIs(t, controller.UpdateFanSpeed(), ErrPwmWriteFailed)
}

func TestFanController_Run_PersistentWriteFailure_RestoresAndStopsOnce(t *testing.T) {
	defer setUpRunTestConfig(time.Hour)()

	// GIVEN a fan in automatic mode at PWM 100, whose PWM can't be written
	fan := newPanickingFan()
	fan.setPwmAlwaysFails = true
	curveValue := 150.0
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := newRunTestController(fan, MockCurve{ID: "curve", Value: &curveValue}, 200*time.Millisecond, clock)
	var notifiedMu sync.Mutex
	var notified []error
	var writesWhenNotified [][]fans.ControlMode
	controller.errorNotifier = func(title, format string, a ...any) {
		notifiedMu.Lock()
		defer notifiedMu.Unlock()
		for _, arg := range a {
			if err, ok := arg.(error); ok {
				notified = append(notified, err)
			}
		}
		writesWhenNotified = append(writesWhenNotified, fan.getControlModeWrites())
	}

	// WHEN
	done := startRun(controller, context.Background())
	advanceThroughRunStartup(t, clock)
	deadline := time.After(5 * time.Second)
	var err error
	for running := true; running; {
		select {
		case err = <-done:
			running = false
		case <-deadline:
			t.Fatal("Run() did not return after writing the PWM value kept failing")
		default:
			clock.Advance(200 * time.Millisecond)
		}
	}

	// THEN: the controller stopped, and restored the fan once, before notifying the user once
	assert.NoError(t, err)
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, fan.getControlModeWrites())
	notifiedMu.Lock()
	defer notifiedMu.Unlock()
	require.Len(t, notified, 1)
	assert.ErrorIs(t, notified[0], ErrPwmWriteFailed)
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, writesWhenNotified[0])
	// Run returning nil doesn't overwrite why the controller stopped
	assertState(t, controller, status.StateStoppedPwmWriteFailed, ErrPwmWriteFailed.Error())
}

func TestFanController_UpdateFanSpeed_WriteFailuresBeforeCurveFailure_DontCountAfterRecovery(t *testing.T) {
	// GIVEN a fan whose PWM can't be written for a few seconds
	fan := &MockFan{ID: "fan", setPwmAlwaysFails: true}
	curveValue := 100.0
	controller, clock := newPwmWriteFailureTestController(fan, &curveValue)
	curve := &switchableCurve{value: 100}
	controller.curve = curve
	controller.errorNotifier = func(title, format string, a ...any) {}
	for i := 0; i < 5; i++ {
		require.NoError(t, controller.UpdateFanSpeed())
		clock.Advance(time.Second)
	}

	// WHEN the curve can't be evaluated for a while (onSensorFailure: max)
	curve.set(0, errStale)
	for i := 0; i < 60; i++ {
		assert.ErrorIs(t, controller.UpdateFanSpeed(), ErrCurveEvaluation)
		clock.Advance(time.Second)
	}

	// THEN after the recovery, a failed write starts a new series instead of stopping the controller at once
	curve.set(100, nil)
	assert.NoError(t, controller.UpdateFanSpeed())
	assert.Equal(t, 1, controller.pwmWriteFailures.count)
}

func TestConsecutiveFailures_LogsFirstEveryNthAndCountsDuration(t *testing.T) {
	// GIVEN
	var failures consecutiveFailures
	start := time.Unix(1000, 0)

	// WHEN / THEN: the first failure and every failureLogInterval-th failure are logged
	var logged []int
	for n := 1; n <= 3*failureLogInterval; n++ {
		count, duration, log := failures.fail(start.Add(time.Duration(n-1) * time.Second))
		assert.Equal(t, n, count)
		assert.Equal(t, time.Duration(n-1)*time.Second, duration)
		if log {
			logged = append(logged, n)
		}
	}
	assert.Equal(t, []int{1, failureLogInterval, 2 * failureLogInterval, 3 * failureLogInterval}, logged)

	// WHEN it succeeds, THEN it reports the number of failures before (so the recovery can be logged), once
	assert.Equal(t, 3*failureLogInterval, failures.succeed())
	assert.Equal(t, 0, failures.succeed())

	// WHEN it fails again, THEN that is a new series
	count, duration, log := failures.fail(start.Add(time.Hour))
	assert.Equal(t, 1, count)
	assert.Equal(t, time.Duration(0), duration)
	assert.True(t, log)
}

func TestFanController_ThirdPartyPwmChange_IsCountedOnEveryCycle(t *testing.T) {
	// GIVEN a fan that was set to 100 by the controller
	fan := &MockFan{ID: "fan"}
	curveValue := 100.0
	controller, clock := newPwmWriteFailureTestController(fan, &curveValue)
	require.NoError(t, controller.UpdateFanSpeed())

	// WHEN a third party changes the PWM value again after each restore (each change is confirmed by a second read
	// on the next update, after the confirmation delay)
	log := captureLog(t, func() {
		for i := 0; i < 2*failureLogInterval; i++ {
			fan.mu.Lock()
			fan.PWM = 50
			fan.mu.Unlock()
			require.NoError(t, controller.UpdateFanSpeed())
			clock.Advance(minThirdPartyConfirmationDelay)
			require.NoError(t, controller.UpdateFanSpeed())
			require.Equal(t, 100, fan.PWM, "restored after the confirmation")
		}
	})

	// THEN every change is counted, the warning is rate-limited (first and every 25th), and the check recovers afterwards
	assert.Equal(t, 2*failureLogInterval, controller.GetStatistics().UnexpectedPwmValueCount)
	assert.Equal(t, 2*failureLogInterval, controller.thirdPartyPwmChanges.count)
	assert.Equal(t, 3, strings.Count(log, "was changed by third party"), log)
	assert.Contains(t, log, "changes in a row: 1;")
	assert.Contains(t, log, "changes in a row: 25;")
	assert.Contains(t, log, "changes in a row: 50;")
	log = captureLog(t, func() {
		require.NoError(t, controller.UpdateFanSpeed())
	})
	assert.Equal(t, 0, controller.thirdPartyPwmChanges.count)
	assert.Contains(t, log, "matches the value set by fan2go again, after 50 unexpected values in a row")
}
