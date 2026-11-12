package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller/status"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// switchableCurve is a curve whose result (value and error) can be changed while a controller reads it.
type switchableCurve struct {
	mu    sync.Mutex
	value float64
	err   error
	// number of reads of the result (see CurrentResult)
	evaluations int
}

func (c *switchableCurve) GetId() string { return "curve" }

// Evaluate panics: only the curve evaluator evaluates curves, a fan controller only reads their result.
func (c *switchableCurve) Evaluate() (float64, error) {
	panic("fan controllers must not evaluate curves")
}

// CurrentResult counts the reads of the result: the controller reads it once per update.
func (c *switchableCurve) CurrentResult() curves.CurveResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evaluations++
	return curves.CurveResult{Value: c.value, Err: c.err, EvaluatedAt: time.Unix(1, 0)}
}

func (c *switchableCurve) getEvaluations() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evaluations
}

func (c *switchableCurve) CurrentValue() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func (c *switchableCurve) set(value float64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = value
	c.err = err
}

var errStale = fmt.Errorf("curve curve: sensor s: %w", sensors.ErrSensorStale)

// sensorFailureTest runs a controller for a panickingFan (automatic mode before the start, PWM 100,
// identity pwmMap, unscaled curve values) with a switchableCurve.
type sensorFailureTest struct {
	t          *testing.T
	fan        *panickingFan
	curve      *switchableCurve
	clock      *util.FakeClock
	controller *DefaultFanController
	cancel     context.CancelFunc
	done       <-chan error

	notifiedMu sync.Mutex
	notified   []string
}

func startSensorFailureTest(t *testing.T, onSensorFailure configuration.OnSensorFailureAction, onExit *configuration.OnExitConfig) *sensorFailureTest {
	t.Cleanup(setUpRunTestConfig(time.Hour))
	test := &sensorFailureTest{t: t}
	test.fan = newPanickingFan()
	test.fan.MaxPWM = 200
	test.fan.onSensorFailure = onSensorFailure
	if onExit != nil {
		test.fan.ControlModeConfig = &configuration.ControlModeConfig{OnExit: onExit}
	}
	test.curve = &switchableCurve{value: 50}
	test.clock = util.NewFakeClock(time.Unix(1000, 0))
	test.controller = newRunTestController(test.fan, test.curve, time.Millisecond, test.clock)
	test.controller.errorNotifier = func(title, format string, a ...any) {
		test.notifiedMu.Lock()
		defer test.notifiedMu.Unlock()
		test.notified = append(test.notified, fmt.Sprintf(format, a...))
	}
	ctx, cancel := context.WithCancel(context.Background())
	test.cancel = cancel
	test.done = startRun(test.controller, ctx)
	advanceThroughRunStartup(t, test.clock)
	test.awaitPwm(50)
	return test
}

// awaitPwm ticks the controller until the fan has the given PWM value.
func (test *sensorFailureTest) awaitPwm(pwm int) {
	test.t.Helper()
	require.Eventually(test.t, func() bool {
		test.clock.Advance(time.Millisecond)
		current, _ := test.fan.GetPwm()
		return current == pwm
	}, 5*time.Second, time.Millisecond, "fan PWM never became %d", pwm)
}

// tick lets the controller run at least n more update cycles.
func (test *sensorFailureTest) tick(n int) {
	test.t.Helper()
	target := test.curve.getEvaluations() + n
	require.Eventually(test.t, func() bool {
		test.clock.Advance(time.Millisecond)
		return test.curve.getEvaluations() >= target
	}, 5*time.Second, time.Millisecond)
}

// awaitNotified ticks the controller until the user was notified n times.
func (test *sensorFailureTest) awaitNotified(n int) {
	test.t.Helper()
	require.Eventually(test.t, func() bool {
		test.clock.Advance(time.Millisecond)
		return len(test.getNotified()) >= n
	}, 5*time.Second, time.Millisecond, "never notified %d times", n)
}

func (test *sensorFailureTest) getNotified() []string {
	test.notifiedMu.Lock()
	defer test.notifiedMu.Unlock()
	return append([]string{}, test.notified...)
}

func (test *sensorFailureTest) stop() {
	test.t.Helper()
	test.cancel()
	assert.NoError(test.t, awaitRun(test.t, test.done))
}

func TestFanController_SensorFailure_Max_SetsMaxPwmAndRecovers(t *testing.T) {
	for _, onSensorFailure := range []configuration.OnSensorFailureAction{"", configuration.OnSensorFailureMax} {
		t.Run(fmt.Sprintf("onSensorFailure=%q", onSensorFailure), func(t *testing.T) {
			test := startSensorFailureTest(t, onSensorFailure, nil)

			assert.Equal(t, status.StateRunning, test.controller.GetStatus().State)

			// WHEN: the curve fails
			test.curve.set(0, errStale)

			// THEN: the fan goes to its maxPwm, and the user is notified once
			test.awaitPwm(200)
			test.tick(5)
			assert.Len(t, test.getNotified(), 1)
			assertState(t, test.controller, status.StateSensorFailureMax, "sensor value is stale")
			assert.Contains(t, test.getNotified()[0], "max PWM")
			assert.Equal(t, []fans.ControlMode{fans.ControlModePWM}, test.fan.getControlModeWrites())
			// the statistics report the max PWM as the last PWM set by fan2go
			lastSetPwm, ok := test.controller.GetLastSetPwm()
			assert.True(t, ok)
			assert.Equal(t, 200, lastSetPwm)

			// WHEN: the curve recovers, normal control resumes
			test.curve.set(80, nil)
			test.awaitPwm(80)
			assertState(t, test.controller, status.StateRunning, "")

			// a second failure notifies again
			test.curve.set(0, errStale)
			test.awaitPwm(200)
			// the notification follows the write in the same update cycle
			test.tick(1)
			assert.Len(t, test.getNotified(), 2)

			// on exit the fan is restored as usual
			test.stop()
			assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, test.fan.getControlModeWrites())
			assertState(t, test.controller, status.StateStopped, "")
		})
	}
}

func TestFanController_SensorFailure_Restore_HandsFanBackAndRetakesControl(t *testing.T) {
	test := startSensorFailureTest(t, configuration.OnSensorFailureRestore, nil)

	// WHEN: the curve fails
	test.curve.set(0, errStale)

	// THEN: the fan is handed back to its original (automatic) control mode, once
	test.awaitNotified(1)
	assertState(t, test.controller, status.StateSensorFailureRestored, "sensor value is stale")
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, test.fan.getControlModeWrites())
	writes := test.fan.getWrites()
	test.tick(5)
	assert.Equal(t, writes, test.fan.getWrites(), "fan2go leaves the fan alone while the curve fails")
	assert.Len(t, test.getNotified(), 1)

	// the BIOS changes the speed of the handed back fan
	require.NoError(t, test.fan.MockFan.SetPwm(120))

	// WHEN: the curve recovers
	test.curve.set(80, nil)

	// THEN: manual control is taken again, and normal control resumes
	test.awaitPwm(80)
	assertState(t, test.controller, status.StateRunning, "")
	assert.Equal(t, 0, test.controller.GetStatistics().UnexpectedPwmValueCount, "a PWM value set while handed back is no third party change")
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic, fans.ControlModePWM}, test.fan.getControlModeWrites())

	// a second failure restores again
	test.curve.set(0, errStale)
	test.awaitNotified(2)
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic, fans.ControlModePWM, fans.ControlModeAutomatic},
		test.fan.getControlModeWrites())

	// exit while the fan is handed back doesn't restore it a second time
	test.stop()
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic, fans.ControlModePWM, fans.ControlModeAutomatic},
		test.fan.getControlModeWrites())
}

func TestFanController_SensorFailure_Restore_RecoveredFanIsRestoredOnExit(t *testing.T) {
	test := startSensorFailureTest(t, configuration.OnSensorFailureRestore, nil)
	test.curve.set(0, errStale)
	test.awaitNotified(1)
	test.curve.set(80, nil)
	test.awaitPwm(80)

	test.stop()

	// the restore on exit happens again, after fan2go took control again
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic, fans.ControlModePWM, fans.ControlModeAutomatic},
		test.fan.getControlModeWrites())
}

// If restoring wouldn't hand the fan over to another control mode (here: controlMode.onExit: none),
// it is set to its max PWM instead.
func TestFanController_SensorFailure_RestoreWritesNothing_FallsBackToMax(t *testing.T) {
	test := startSensorFailureTest(t, configuration.OnSensorFailureRestore, &configuration.OnExitConfig{None: &configuration.OnExitNoneConfig{}})

	test.curve.set(0, errStale)

	test.awaitPwm(200)
	test.tick(1)
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM}, test.fan.getControlModeWrites())
	require.Len(t, test.getNotified(), 1)
	assert.Contains(t, test.getNotified()[0], "max PWM")
	assertState(t, test.controller, status.StateSensorFailureMax, "sensor value is stale")

	test.curve.set(80, nil)
	test.awaitPwm(80)
	test.stop()
}

// The failure state is only used by the update loop, but the controller statistics and the fan
// are read concurrently while it changes.
func TestFanController_SensorFailure_ConcurrentReads(t *testing.T) {
	test := startSensorFailureTest(t, configuration.OnSensorFailureRestore, nil)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = test.controller.GetStatistics()
				_ = test.controller.GetStatus()
				_, _ = test.fan.GetPwm()
				_, _ = test.fan.GetControlMode()
			}
		}
	}()
	for i := 0; i < 3; i++ {
		test.curve.set(0, errors.New("evaluation failed"))
		test.awaitNotified(i + 1)
		test.curve.set(float64(60+i), nil)
		test.awaitPwm(60 + i)
	}
	close(stop)
	wg.Wait()
	test.stop()
}

func TestRestoreControlMode_ReportsWhetherItHandedTheFanOver(t *testing.T) {
	pwm := 100
	auto := fans.ControlModeAutomatic

	fan := newPanickingFan()
	controller := &DefaultFanController{fan: fan, originalFanState: &FanStateSnapshot{ControlMode: &auto, PwmValue: &pwm}}
	assert.True(t, controller.restoreControlMode())

	controller = &DefaultFanController{fan: fan}
	assert.False(t, controller.restoreControlMode(), "original state never captured")

	fan = newPanickingFan()
	fan.ControlModeConfig = &configuration.ControlModeConfig{OnExit: &configuration.OnExitConfig{None: &configuration.OnExitNoneConfig{}}}
	controller = &DefaultFanController{fan: fan, originalFanState: &FanStateSnapshot{ControlMode: &auto, PwmValue: &pwm}}
	assert.False(t, controller.restoreControlMode(), "onExit: none")
	assert.Empty(t, fan.getWrites())

	// a fan without control mode support, whose PWM value is known
	pwmOnlyFan := &restoreTestFan{
		MockFan:  MockFan{ID: "fan", PWM: 180},
		features: map[fans.FeatureFlag]bool{fans.FeaturePwmSensor: true},
	}
	controller = &DefaultFanController{fan: pwmOnlyFan}
	require.NoError(t, controller.storeCurrentFanState())
	assert.False(t, controller.restoreControlMode(), "only the original PWM value is written, the fan stays at a fixed speed")
	assert.Equal(t, []int{180}, pwmOnlyFan.pwmWrites)

	// a fan whose original control mode was manual PWM
	manual := fans.ControlModePWM
	fan = newPanickingFan()
	controller = &DefaultFanController{fan: fan, originalFanState: &FanStateSnapshot{ControlMode: &manual, PwmValue: &pwm}}
	assert.False(t, controller.restoreControlMode(), "original mode is manual PWM")
	assert.Equal(t, []string{"mode", "pwm"}, fan.getWrites())

	// a fan whose original control mode is unknown, and that rejects automatic control:
	// it falls back to manual mode at the original PWM value
	rejectsAutoFan := &restoreTestFan{
		MockFan:             MockFan{ID: "fan", PWM: 180},
		features:            allFanFeatures,
		failingControlModes: map[fans.ControlMode]bool{fans.ControlModeAutomatic: true},
	}
	controller = &DefaultFanController{fan: rejectsAutoFan, originalFanState: &FanStateSnapshot{PwmValue: &pwm}}
	assert.False(t, controller.restoreControlMode(), "fell back to a fixed PWM value")
	assert.Equal(t, []int{100}, rejectsAutoFan.pwmWrites)

	// ... and that accepts automatic control
	acceptsAutoFan := &restoreTestFan{MockFan: MockFan{ID: "fan", PWM: 180}, features: allFanFeatures}
	controller = &DefaultFanController{fan: acceptsAutoFan, originalFanState: &FanStateSnapshot{PwmValue: &pwm}}
	assert.True(t, controller.restoreControlMode(), "handed back to automatic control")

	// a fan without control mode support and without PWM readback (e.g. a cmd fan without getPwm)
	unknownFan := &restoreTestFan{MockFan: MockFan{ID: "fan", PWM: 180}, features: map[fans.FeatureFlag]bool{}}
	controller = &DefaultFanController{fan: unknownFan}
	require.NoError(t, controller.storeCurrentFanState())
	assert.False(t, controller.restoreControlMode(), "nothing known, nothing written")
	assert.Empty(t, unknownFan.pwmWrites)
}

// A fan whose original control mode is manual PWM would only get a fixed PWM value back, so it is set
// to its max PWM instead.
func TestFanController_SensorFailure_RestoreToManualMode_FallsBackToMax(t *testing.T) {
	t.Cleanup(setUpRunTestConfig(time.Hour))
	fan := newPanickingFan()
	fan.ControlMode = fans.ControlModePWM
	fan.MaxPWM = 200
	fan.onSensorFailure = configuration.OnSensorFailureRestore
	curve := &switchableCurve{value: 50}
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := newRunTestController(fan, curve, time.Millisecond, clock)
	controller.errorNotifier = func(title, format string, a ...any) {}
	ctx, cancel := context.WithCancel(context.Background())
	done := startRun(controller, ctx)
	advanceThroughRunStartup(t, clock)
	test := &sensorFailureTest{t: t, fan: fan, curve: curve, clock: clock, controller: controller, cancel: cancel, done: done}
	test.awaitPwm(50)

	curve.set(0, errStale)

	test.awaitPwm(200)
	test.tick(3)
	pwm, _ := fan.GetPwm()
	assert.Equal(t, 200, pwm)
	test.stop()
}

// assertState asserts the state of the controller, and that its error contains errorPart ("" means no error).
func assertState(t *testing.T, controller *DefaultFanController, state status.State, errorPart string) {
	t.Helper()
	current := controller.GetStatus()
	assert.Equal(t, "fan", current.FanId)
	assert.Equal(t, state, current.State)
	assert.False(t, current.Since.IsZero())
	if errorPart == "" {
		assert.Empty(t, current.Error)
	} else {
		assert.Contains(t, current.Error, errorPart)
	}
}

// After a failed config reload, Run is called again on the same controller. If the curve still fails,
// the state must say so, although the failure state was entered in the previous run.
func TestFanController_SensorFailure_Max_StateAfterRunStartsAgain(t *testing.T) {
	test := startSensorFailureTest(t, configuration.OnSensorFailureMax, nil)
	test.curve.set(0, errStale)
	test.awaitPwm(200)
	test.stop()
	assertState(t, test.controller, status.StateStopped, "")

	// WHEN: Run starts again while the curve still fails
	ctx, cancel := context.WithCancel(context.Background())
	test.cancel = cancel
	test.done = startRun(test.controller, ctx)
	test.clock.BlockUntil(1)
	test.clock.Advance(defaultStartupSensorWarmup + configuration.CurrentConfig.TempSensorPollingRate)
	// manual control is taken, the update loop waits for its startup delay: no update yet,
	// so the controller must not report running (alerts on sensor_failure_* would resolve and fire again)
	test.clock.BlockUntil(2)
	assert.Equal(t, status.StateInitializing, test.controller.GetStatus().State)
	test.clock.Advance(defaultStartupUpdateDelay)
	test.awaitPwm(200)
	test.tick(2)

	// THEN
	assertState(t, test.controller, status.StateSensorFailureMax, "sensor value is stale")
	test.stop()
}
