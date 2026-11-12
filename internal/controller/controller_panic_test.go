package controller

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// panickingFan is a MockFan that is in automatic mode before the controller starts, records its
// writes, and panics in the configured methods.
type panickingFan struct {
	MockFan
	panicOnSetPwm         bool
	panicOnGetRpm         bool
	panicOnAttachCurve    bool
	panicOnGetControlMode bool

	// if set, the first SetPwm call blocks until the RPM monitor panicked, see
	// TestFanController_Run_PanicInRpmMonitor_DoesNotRestoreBeforeUpdateLoopStopped
	rpmPanicDuringSetPwm *rpmPanicDuringSetPwm

	writesMu          sync.Mutex
	controlModeWrites []fans.ControlMode
	// all writes in order: "pwm" or "mode"
	writes []string
}

type rpmPanicDuringSetPwm struct {
	setPwmEntered chan struct{}
	rpmPanicked   chan struct{}
	// closed on the first control mode write after the RPM monitor panicked
	restored  chan struct{}
	enterOnce sync.Once
	restoreMu sync.Mutex
}

func newPanickingFan() *panickingFan {
	return &panickingFan{
		MockFan: MockFan{
			ID:                     "fan",
			ControlMode:            fans.ControlModeAutomatic,
			PWM:                    100,
			MinPWM:                 0,
			MaxPWM:                 255,
			RPM:                    1000,
			useUnscaledCurveValues: true,
			speedCurve:             &map[int]float64{100: 1000},
			// no automatic setPwm -> getPwm detection before the control loop starts
			PwmMap:            &configuration.PwmMapConfig{Identity: &configuration.PwmMapIdentityConfig{}},
			SetPwmToGetPwmMap: &configuration.SetPwmToGetPwmMapConfig{Identity: &configuration.SetPwmToGetPwmMapIdentityConfig{}},
		},
	}
}

func (fan *panickingFan) SetPwm(pwm int) error {
	if fan.panicOnSetPwm {
		panic("SetPwm exploded")
	}
	if p := fan.rpmPanicDuringSetPwm; p != nil {
		first := false
		p.enterOnce.Do(func() { first = true })
		if first {
			close(p.setPwmEntered)
			<-p.rpmPanicked
			// a restore by the RPM monitor would happen now; give it a chance to (not) happen
			select {
			case <-p.restored:
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	fan.writesMu.Lock()
	fan.writes = append(fan.writes, "pwm")
	fan.writesMu.Unlock()
	return fan.MockFan.SetPwm(pwm)
}

func (fan *panickingFan) GetRpm() (int, error) {
	if p := fan.rpmPanicDuringSetPwm; p != nil {
		<-p.setPwmEntered
		close(p.rpmPanicked)
		panic("GetRpm exploded")
	}
	if fan.panicOnGetRpm {
		panic("GetRpm exploded")
	}
	return fan.MockFan.GetRpm()
}

func (fan *panickingFan) GetControlMode() (fans.ControlMode, error) {
	if fan.panicOnGetControlMode {
		panic("GetControlMode exploded")
	}
	return fan.MockFan.GetControlMode()
}

func (fan *panickingFan) AttachFanRpmCurveData(curveData *map[int]float64) error {
	if fan.panicOnAttachCurve {
		panic("AttachFanRpmCurveData exploded")
	}
	return fan.MockFan.AttachFanRpmCurveData(curveData)
}

func (fan *panickingFan) SetControlMode(mode fans.ControlMode) error {
	fan.writesMu.Lock()
	fan.controlModeWrites = append(fan.controlModeWrites, mode)
	fan.writes = append(fan.writes, "mode")
	fan.writesMu.Unlock()
	if p := fan.rpmPanicDuringSetPwm; p != nil && mode == fans.ControlModeAutomatic {
		p.restoreMu.Lock()
		select {
		case <-p.restored:
		default:
			close(p.restored)
		}
		p.restoreMu.Unlock()
	}
	return fan.MockFan.SetControlMode(mode)
}

func (fan *panickingFan) getWrites() []string {
	fan.writesMu.Lock()
	defer fan.writesMu.Unlock()
	return append([]string{}, fan.writes...)
}

func (fan *panickingFan) getControlModeWrites() []fans.ControlMode {
	fan.writesMu.Lock()
	defer fan.writesMu.Unlock()
	return append([]fans.ControlMode{}, fan.controlModeWrites...)
}

// setUpRunTestConfig configures short polling rates for a Run test and returns a function
// that restores the previous config.
func setUpRunTestConfig(rpmPollingRate time.Duration) func() {
	originalConfig := configuration.CurrentConfig
	configuration.CurrentConfig.TempSensorPollingRate = time.Millisecond
	configuration.CurrentConfig.RpmPollingRate = rpmPollingRate
	configuration.CurrentConfig.FanController.PwmSetDelay = 0
	configuration.CurrentConfig.RpmRollingWindowSize = 10
	return func() {
		configuration.CurrentConfig = originalConfig
	}
}

func newRunTestController(fan fans.Fan, curve MockCurve, updateRate time.Duration, clock *util.FakeClock) *DefaultFanController {
	controller := NewFanController(
		mockPersistence{hasSavedPwmData: true},
		fan,
		curve,
		control_loop.NewDirectControlLoop(nil),
		updateRate,
		false,
	)
	controller.SetClock(clock)
	return controller
}

func startRun(controller *DefaultFanController, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- controller.Run(ctx)
	}()
	return done
}

func awaitRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return")
		return nil
	}
}

func TestFanController_Run_PanicInUpdateLoop_RestoresFanAndReturnsError(t *testing.T) {
	defer setUpRunTestConfig(time.Hour)()

	// GIVEN: a fan that panics when the control loop sets its speed
	fan := newPanickingFan()
	fan.panicOnSetPwm = true
	curveValue := 200.0
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := newRunTestController(fan, MockCurve{ID: "curve", Value: &curveValue}, time.Millisecond, clock)

	// WHEN
	done := startRun(controller, context.Background())
	advanceThroughRunStartup(t, clock)
	err := awaitRun(t, done)

	// THEN: the panic is returned as an error, and the fan is handed back to automatic control once
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panic in update loop")
	assert.Contains(t, err.Error(), "SetPwm exploded")
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, fan.getControlModeWrites())
}

func TestFanController_Run_PanicInRpmMonitor_RestoresFanOnceAndReturnsError(t *testing.T) {
	defer setUpRunTestConfig(10 * time.Millisecond)()

	// GIVEN: a fan that panics when its RPM is measured
	fan := newPanickingFan()
	fan.panicOnGetRpm = true
	curveValue := 200.0
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := newRunTestController(fan, MockCurve{ID: "curve", Value: &curveValue}, time.Hour, clock)

	// WHEN: the RPM monitor ticks (latest during the startup delay of the update loop)
	done := startRun(controller, context.Background())
	advanceThroughRunStartup(t, clock)
	err := awaitRun(t, done)

	// THEN: the panic is returned as an error, and the fan is restored only once,
	// although the update loop stops (and restores) because of it, too
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panic in RPM monitor")
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, fan.getControlModeWrites())
}

func TestFanController_Run_PanicInRpmMonitor_DoesNotRestoreBeforeUpdateLoopStopped(t *testing.T) {
	defer setUpRunTestConfig(10 * time.Millisecond)()

	// GIVEN: the RPM monitor panics while the update loop is setting the fan speed
	fan := newPanickingFan()
	fan.rpmPanicDuringSetPwm = &rpmPanicDuringSetPwm{
		setPwmEntered: make(chan struct{}),
		rpmPanicked:   make(chan struct{}),
		restored:      make(chan struct{}),
	}
	curveValue := 200.0
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := newRunTestController(fan, MockCurve{ID: "curve", Value: &curveValue}, time.Hour, clock)

	// WHEN
	done := startRun(controller, context.Background())
	advanceThroughRunStartup(t, clock)
	// the RPM monitor ticks
	clock.Advance(10 * time.Millisecond)
	err := awaitRun(t, done)

	// THEN: the fan is restored after the last speed was set, not in between
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panic in RPM monitor")
	assert.Equal(t, []string{"mode", "pwm", "mode"}, fan.getWrites())
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, fan.getControlModeWrites())
}

// panickingClock panics when Run waits during its sensor warm-up.
type panickingClock struct {
	*util.FakeClock
}

func (c panickingClock) After(d time.Duration) <-chan time.Time {
	panic("After exploded")
}

func TestFanController_Run_PanicBeforeFanIsTouched_ReturnsErrorWithoutWritingToFan(t *testing.T) {
	defer setUpRunTestConfig(time.Hour)()

	// GIVEN: a panic after the fan state was captured, but before fan2go wrote to the fan
	fan := newPanickingFan()
	curveValue := 200.0
	controller := newRunTestController(fan, MockCurve{ID: "curve", Value: &curveValue}, time.Millisecond, util.NewFakeClock(time.Unix(1000, 0)))
	controller.SetClock(panickingClock{util.NewFakeClock(time.Unix(1000, 0))})

	// WHEN
	err := awaitRun(t, startRun(controller, context.Background()))

	// THEN
	require.Error(t, err)
	assert.Contains(t, err.Error(), "After exploded")
	assert.NotNil(t, controller.originalFanState)
	assert.Empty(t, fan.getWrites())
}

func TestFanController_Run_PanicWhileCapturingFanState_ReturnsError(t *testing.T) {
	defer setUpRunTestConfig(time.Hour)()

	// GIVEN: a fan that panics while its state is captured
	fan := newPanickingFan()
	fan.panicOnGetControlMode = true
	curveValue := 200.0
	controller := newRunTestController(fan, MockCurve{ID: "curve", Value: &curveValue}, time.Millisecond, util.NewFakeClock(time.Unix(1000, 0)))

	// WHEN
	err := awaitRun(t, startRun(controller, context.Background()))

	// THEN
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GetControlMode exploded")
	assert.Empty(t, fan.getWrites())
}

func TestFanController_Run_PanicBeforeControlLoop_RestoresFanAndReturnsError(t *testing.T) {
	defer setUpRunTestConfig(time.Hour)()

	// GIVEN: a fan that panics while the controller prepares the control loop
	fan := newPanickingFan()
	fan.panicOnAttachCurve = true
	curveValue := 200.0
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := newRunTestController(fan, MockCurve{ID: "curve", Value: &curveValue}, time.Millisecond, clock)

	// WHEN
	done := startRun(controller, context.Background())
	clock.BlockUntil(1)
	clock.Advance(defaultStartupSensorWarmup + configuration.CurrentConfig.TempSensorPollingRate)
	err := awaitRun(t, done)

	// THEN
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panic in fan controller")
	assert.Equal(t, []fans.ControlMode{fans.ControlModeAutomatic}, fan.getControlModeWrites())
}

func TestFanController_Run_UpdateError_RestoresFanBeforeNotifying(t *testing.T) {
	defer setUpRunTestConfig(time.Hour)()

	// GIVEN: a curve that fails to evaluate
	fan := newPanickingFan()
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := newRunTestController(fan, MockCurve{ID: "curve", Err: errors.New("evaluation failed")}, time.Millisecond, clock)
	var writesWhenNotified [][]fans.ControlMode
	var notifiedMu sync.Mutex
	controller.errorNotifier = func(title, format string, a ...any) {
		notifiedMu.Lock()
		defer notifiedMu.Unlock()
		writesWhenNotified = append(writesWhenNotified, fan.getControlModeWrites())
	}

	// WHEN
	done := startRun(controller, context.Background())
	advanceThroughRunStartup(t, clock)
	err := awaitRun(t, done)

	// THEN: the controller stops, and the fan was already restored when the user was notified
	assert.NoError(t, err)
	notifiedMu.Lock()
	defer notifiedMu.Unlock()
	require.Len(t, writesWhenNotified, 1)
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, writesWhenNotified[0])
}

func TestFanController_Run_Cancelled_RestoresFanOnce(t *testing.T) {
	defer setUpRunTestConfig(time.Hour)()

	// GIVEN: a running controller
	fan := newPanickingFan()
	curveValue := 200.0
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := newRunTestController(fan, MockCurve{ID: "curve", Value: &curveValue}, time.Hour, clock)
	ctx, cancel := context.WithCancel(context.Background())
	done := startRun(controller, ctx)
	advanceThroughRunStartup(t, clock)
	// the update loop waits for its ticker
	clock.BlockUntil(2)

	// WHEN
	cancel()
	err := awaitRun(t, done)

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, fan.getControlModeWrites())
}

func TestRestoreControlModeOnce_PanicDuringRestore_IsRecovered(t *testing.T) {
	// GIVEN: a fan that panics when its original PWM value is written back
	fan := newPanickingFan()
	fan.ControlMode = fans.ControlModePWM
	fan.panicOnSetPwm = true
	pwm := 100
	mode := fans.ControlModePWM
	controller := &DefaultFanController{
		fan:              fan,
		originalFanState: &FanStateSnapshot{ControlMode: &mode, PwmValue: &pwm},
		restoreOnce:      &sync.Once{},
	}

	// WHEN / THEN
	assert.NotPanics(t, controller.restoreControlModeOnce)
	// a second call doesn't restore again
	controller.restoreControlModeOnce()
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM}, fan.getControlModeWrites())
}

func TestFanController_Run_FanStalledAtMaxPwm_RestoresFanBeforeNotifying(t *testing.T) {
	defer setUpRunTestConfig(time.Hour)()

	// GIVEN: a fan that should never stop, runs at its max PWM and reports 0 RPM
	fan := newPanickingFan()
	fan.MinPWM = 20
	fan.MaxPWM = 100
	fan.RPM = 0
	fan.shouldNeverStop = true
	curveValue := 100.0
	lastTarget := 100
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := newRunTestController(fan, MockCurve{ID: "curve", Value: &curveValue}, time.Millisecond, clock)
	controller.lastTarget = &lastTarget
	var notified []error
	var writesWhenNotified [][]fans.ControlMode
	var notifiedMu sync.Mutex
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
	// the RPM monitor doesn't tick before the first update: seed the RPM average (0 RPM) by hand
	controller.measureRpm(fan)

	// WHEN: the fan had enough time to spin up, but didn't
	done := startRun(controller, context.Background())
	advanceThroughRunStartup(t, clock)
	deadline := time.After(5 * time.Second)
	var err error
	for running := true; running; {
		select {
		case err = <-done:
			running = false
		case <-deadline:
			t.Fatal("Run() did not return after the fan stalled at max pwm")
		default:
			clock.Advance(time.Minute)
		}
	}

	// THEN: the stall stopped the controller, and the fan was restored before the user was notified
	assert.NoError(t, err)
	notifiedMu.Lock()
	defer notifiedMu.Unlock()
	require.Len(t, notified, 1)
	assert.ErrorIs(t, notified[0], ErrFanStalledAtMaxPwm)
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, writesWhenNotified[0])
}
