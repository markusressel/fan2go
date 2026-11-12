package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/controller/status"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFanController_GetStatus_BeforeRun(t *testing.T) {
	controller := NewFanController(nil, &MockFan{ID: "fan"}, nil, nil, time.Second, false)

	assert.Equal(t, status.Status{FanId: "fan", State: status.StateStopped}, controller.GetStatus())
}

func TestFanController_SetState_KeepsSinceWhileTheStateDoesntChange(t *testing.T) {
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := NewFanController(nil, &MockFan{ID: "fan"}, nil, nil, time.Second, false)
	controller.SetClock(clock)

	controller.setState(status.StateSensorFailureMax, errors.New("first"))
	clock.Advance(time.Second)
	controller.setState(status.StateSensorFailureMax, errors.New("second"))

	assert.Equal(t, status.Status{FanId: "fan", State: status.StateSensorFailureMax, Since: time.Unix(1000, 0), Error: "second"},
		controller.GetStatus())

	controller.setState(status.StateRunning, nil)
	assert.Equal(t, status.Status{FanId: "fan", State: status.StateRunning, Since: time.Unix(1001, 0)}, controller.GetStatus())
}

func TestFanController_StoppedStates(t *testing.T) {
	tests := []struct {
		err      error
		expected status.State
	}{
		{err: nil, expected: status.StateStopped},
		// shutdown or config reload
		{err: context.Canceled, expected: status.StateStopped},
		{err: fmt.Errorf("%w for fan", ErrPwmWriteFailed), expected: status.StateStoppedPwmWriteFailed},
		{err: ErrFanStalledAtMaxPwm, expected: status.StateStoppedStalled},
		{err: errors.New("initialization failed"), expected: status.StateStoppedError},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v", tt.err), func(t *testing.T) {
			controller := NewFanController(nil, &MockFan{ID: "fan"}, nil, nil, time.Second, false)
			controller.setState(status.StateRunning, nil)

			controller.setStoppedState(tt.err)

			current := controller.GetStatus()
			assert.Equal(t, tt.expected, current.State)
			assert.True(t, current.State.IsStopped())
			if tt.expected == status.StateStopped {
				assert.Empty(t, current.Error)
			} else {
				assert.Equal(t, tt.err.Error(), current.Error)
			}
		})
	}
}

// The update loop records why it stopped the controller (stopAfterUpdateError), and Run returns nil then.
func TestFanController_StopAfterUpdateError_RecordsTheReason(t *testing.T) {
	controller := NewFanController(nil, &MockFan{ID: "fan"}, nil, nil, time.Second, false)
	controller.errorNotifier = func(title, format string, a ...any) {}
	controller.setState(status.StateRunning, nil)

	controller.stopAfterUpdateError(ErrFanStalledAtMaxPwm)
	controller.setStoppedState(nil)

	assert.Equal(t, status.StateStoppedStalled, controller.GetStatus().State)
	assert.Equal(t, ErrFanStalledAtMaxPwm.Error(), controller.GetStatus().Error)
}

func TestState_IsStopped(t *testing.T) {
	var stopped []status.State
	for _, state := range status.AllStates {
		if state.IsStopped() {
			stopped = append(stopped, state)
		}
	}
	assert.ElementsMatch(t, []status.State{
		status.StateStopped, status.StateStoppedPwmWriteFailed, status.StateStoppedStalled, status.StateStoppedError,
	}, stopped)
}

func TestFanController_Run_CancelledDuringSensorWarmup_IsStoppedNotFailed(t *testing.T) {
	// GIVEN
	fan := &mockFanRecordingWrites{MockFan: MockFan{ID: "fan", PWM: 100, ControlMode: fans.ControlModeAutomatic}}
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := NewFanController(mockPersistence{hasSavedPwmData: true}, fan, nil, control_loop.NewDirectControlLoop(nil), time.Second, false)
	controller.SetClock(clock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- controller.Run(ctx)
	}()

	// WHEN: Run waits for the sensor warm-up and gets cancelled
	clock.BlockUntil(1)
	assert.Equal(t, status.StateInitializing, controller.GetStatus().State)
	cancel()

	// THEN
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return")
	}
	assert.Equal(t, status.Status{FanId: "fan", State: status.StateStopped, Since: time.Unix(1000, 0)}, controller.GetStatus())
}

// captureLog returns everything fan2go logs while f runs.
func captureLog(t *testing.T, f func()) string {
	t.Helper()
	var buf bytes.Buffer
	pterm.SetDefaultOutput(&buf)
	defer pterm.SetDefaultOutput(os.Stdout)
	f()
	return buf.String()
}

func TestFanController_MeasureRpm_LogsFailuresOnlyOnceAndTheRecovery(t *testing.T) {
	// GIVEN a fan with a seeded RPM average
	setupStallTestConfig(t)
	s := newStallTestHwMonFan(t, 20, 255)
	controller, _ := newStallTestController(t, s.fan)
	s.setRpm(t, "1000")
	controller.measureRpm(s.fan)

	// WHEN reading the RPM value fails repeatedly
	s.setRpm(t, "not a number")
	failureLog := captureLog(t, func() {
		for i := 0; i < 3*failureLogInterval; i++ {
			controller.measureRpm(s.fan)
		}
	})

	// THEN only the first failure is logged, and the average is unchanged
	assert.Equal(t, 1, strings.Count(failureLog, "Error reading RPM value of fan hwmon"), failureLog)
	assert.Equal(t, 1000.0, s.fan.GetRpmAvg())

	// WHEN it recovers, the recovery is logged once
	s.setRpm(t, "1000")
	recoveryLog := captureLog(t, func() {
		controller.measureRpm(s.fan)
		controller.measureRpm(s.fan)
	})
	assert.Equal(t, 1, strings.Count(recoveryLog, fmt.Sprintf("succeeded again after %d failed readings", 3*failureLogInterval)), recoveryLog)

	// ... and the next failure is logged again
	s.setRpm(t, "not a number")
	require.Contains(t, captureLog(t, func() { controller.measureRpm(s.fan) }), "Error reading RPM value of fan hwmon")
}

type failingInitPersistence struct {
	mockPersistence
}

func (p failingInitPersistence) Init() error { return errors.New("database is locked") }

func TestFanController_Run_InitError_IsStoppedError(t *testing.T) {
	controller := NewFanController(failingInitPersistence{}, &MockFan{ID: "fan"}, nil, control_loop.NewDirectControlLoop(nil), time.Second, false)
	controller.SetClock(util.NewFakeClock(time.Unix(1000, 0)))

	err := controller.Run(context.Background())

	require.Error(t, err)
	assert.Equal(t, status.Status{FanId: "fan", State: status.StateStoppedError, Since: time.Unix(1000, 0), Error: "database is locked"},
		controller.GetStatus())
}
