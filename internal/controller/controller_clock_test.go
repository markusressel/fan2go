package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
)

func TestNewFanController_DefaultTiming(t *testing.T) {
	// WHEN
	controller := NewFanController(mockPersistence{}, &MockFan{ID: "fan"}, nil, control_loop.NewDirectControlLoop(nil), time.Second, false)

	// THEN: the startup delays keep their previous hard-coded values and the real clock is used
	assert.Equal(t, 2*time.Second, controller.startupSensorWarmup)
	assert.Equal(t, 1*time.Second, controller.startupUpdateDelay)
	assert.Equal(t, util.RealClock, controller.getClock())
	assert.Equal(t, 500*time.Millisecond, NewFanCurveAnalyzer(controller).settleSampleInterval)
}

func TestFanController_GetClock_DefaultsToRealClock(t *testing.T) {
	controller := DefaultFanController{}
	assert.Equal(t, util.RealClock, controller.getClock())

	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller.SetClock(clock)
	assert.Equal(t, clock, controller.getClock())
}

// mockFanRecordingWrites records every PWM and control mode write,
// and the time of every PWM write if a clock is set.
type mockFanRecordingWrites struct {
	MockFan
	clock             util.Clock
	pwmWrites         []int
	pwmWriteTimes     []time.Time
	controlModeWrites []fans.ControlMode
}

func (fan *mockFanRecordingWrites) SetPwm(pwm int) error {
	fan.pwmWrites = append(fan.pwmWrites, pwm)
	if fan.clock != nil {
		fan.pwmWriteTimes = append(fan.pwmWriteTimes, fan.clock.Now())
	}
	return fan.MockFan.SetPwm(pwm)
}

func (fan *mockFanRecordingWrites) SetControlMode(value fans.ControlMode) error {
	fan.controlModeWrites = append(fan.controlModeWrites, value)
	return fan.MockFan.SetControlMode(value)
}

func TestFanController_Run_CancelledDuringSensorWarmup_DoesNotTouchFan(t *testing.T) {
	// GIVEN
	fan := &mockFanRecordingWrites{MockFan: MockFan{ID: "fan", PWM: 100, ControlMode: fans.ControlModeAutomatic}}
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := DefaultFanController{
		persistence:         mockPersistence{hasSavedPwmData: true},
		fan:                 fan,
		updateRate:          time.Second,
		controlLoop:         control_loop.NewDirectControlLoop(nil),
		clock:               clock,
		startupSensorWarmup: defaultStartupSensorWarmup,
		startupUpdateDelay:  defaultStartupUpdateDelay,
	}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- controller.Run(ctx)
	}()

	// WHEN: Run waits for the sensor warm-up and gets cancelled
	clock.BlockUntil(1)
	cancel()

	// THEN: Run returns without waiting for the clock and without writing to the fan
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after its context was cancelled during the sensor warm-up")
	}
	assert.Empty(t, fan.pwmWrites)
	assert.Empty(t, fan.controlModeWrites)
}

func TestFanController_Run_WaitsForStartupDelaysOnClock(t *testing.T) {
	// GIVEN
	originalConfig := configuration.CurrentConfig
	defer func() {
		configuration.CurrentConfig = originalConfig
	}()
	configuration.CurrentConfig.TempSensorPollingRate = 200 * time.Millisecond
	configuration.CurrentConfig.RpmPollingRate = 1 * time.Hour
	// the automatic setPwm -> getPwm detection must not wait on the fake clock
	configuration.CurrentConfig.FanController.PwmSetDelay = 0

	curveValue := 0.0
	start := time.Unix(1000, 0)
	clock := util.NewFakeClock(start)
	fan := &mockFanRecordingWrites{MockFan: MockFan{ID: "fan", PWM: 100, RPM: 1000, ControlMode: fans.ControlModePWM}, clock: clock}
	controller := DefaultFanController{
		persistence:         mockPersistence{hasSavedPwmData: true},
		fan:                 fan,
		curve:               MockCurve{ID: "curve", Value: &curveValue},
		updateRate:          time.Second,
		controlLoop:         control_loop.NewDirectControlLoop(nil),
		clock:               clock,
		startupSensorWarmup: defaultStartupSensorWarmup,
		startupUpdateDelay:  defaultStartupUpdateDelay,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- controller.Run(ctx)
	}()

	// WHEN / THEN: the sensor warm-up is startupSensorWarmup + tempSensorPollingRate
	clock.BlockUntil(1)
	clock.Advance(defaultStartupSensorWarmup + 199*time.Millisecond)
	assert.Equal(t, 1, clock.Waiters(), "warm-up must not be over before startupSensorWarmup + tempSensorPollingRate")
	clock.Advance(1 * time.Millisecond)

	warmupDone := start.Add(defaultStartupSensorWarmup + 200*time.Millisecond)

	// the update loop waits startupUpdateDelay before the first update
	clock.BlockUntil(2) // RPM monitor ticker + startup delay of the update loop
	clock.Advance(defaultStartupUpdateDelay - time.Millisecond)
	clock.Advance(time.Millisecond)
	// the update loop creates its ticker right before the first update
	clock.BlockUntil(2) // RPM monitor ticker + update ticker

	// WHEN
	cancel()

	// THEN
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after its context was cancelled")
	}
	// the automatic setPwm -> getPwm detection writes 4 values to measure the readback delay and then all 256
	// values right after the warm-up, the first update writes exactly startupUpdateDelay later
	writesAtWarmupEnd := 0
	var updateWrites []time.Time
	for _, at := range fan.pwmWriteTimes {
		if at.Equal(warmupDone) {
			writesAtWarmupEnd++
		} else {
			updateWrites = append(updateWrites, at)
		}
	}
	assert.Equal(t, 4+256, writesAtWarmupEnd)
	if assert.NotEmpty(t, updateWrites, "the first update must happen after startupUpdateDelay") {
		assert.Equal(t, warmupDone.Add(defaultStartupUpdateDelay), updateWrites[0])
	}
}

func TestFanController_Run_CancelledDuringSetPwmToGetPwmDetection_RestoresFan(t *testing.T) {
	// GIVEN
	originalConfig := configuration.CurrentConfig
	defer func() {
		configuration.CurrentConfig = originalConfig
	}()
	configuration.CurrentConfig.TempSensorPollingRate = 0
	configuration.CurrentConfig.FanController.PwmSetDelay = 10 * time.Millisecond

	fan := &mockFanRecordingWrites{MockFan: MockFan{ID: "fan", PWM: 100, ControlMode: fans.ControlModeAutomatic}}
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := DefaultFanController{
		persistence:         mockPersistence{hasSavedPwmData: true},
		fan:                 fan,
		updateRate:          time.Second,
		controlLoop:         control_loop.NewDirectControlLoop(nil),
		clock:               clock,
		startupSensorWarmup: defaultStartupSensorWarmup,
		startupUpdateDelay:  defaultStartupUpdateDelay,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- controller.Run(ctx)
	}()
	clock.BlockUntil(1)
	clock.Advance(defaultStartupSensorWarmup)

	// WHEN: cancelled while the detection waits pwmSetDelay after its first write
	clock.BlockUntil(1)
	cancel()

	// THEN: Run returns and restores the original (automatic) control mode
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after its context was cancelled")
	}
	// the detection stopped after its first write (255, the first write of the readback delay measurement)
	assert.Equal(t, []int{255}, fan.pwmWrites)
	assert.Equal(t, fans.ControlModeAutomatic, fan.ControlMode)
}

func TestSleepWithContext(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	clock := util.NewFakeClock(time.Unix(1000, 0))

	// zero duration returns immediately, unless the context is already done
	assert.NoError(t, sleepWithContext(context.Background(), clock, 0))
	assert.ErrorIs(t, sleepWithContext(cancelled, clock, 0), context.Canceled)
	// a done context wins even if the clock fires immediately
	assert.ErrorIs(t, sleepWithContext(cancelled, util.NewAutoAdvancingFakeClock(time.Unix(1000, 0)), time.Second), context.Canceled)

	// waits for the clock
	done := make(chan error, 1)
	go func() {
		done <- sleepWithContext(context.Background(), clock, time.Second)
	}()
	clock.BlockUntil(1)
	clock.Advance(time.Second)
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("sleepWithContext did not return after the clock advanced")
	}
}

// mockFanWithThrottledModeCheck counts control mode reads and configures a throttle for the
// FanModeChangedByThirdParty sanity check.
type mockFanWithThrottledModeCheck struct {
	MockFan
	throttle         time.Duration
	controlModeReads int
}

func (fan *mockFanWithThrottledModeCheck) GetConfig() configuration.FanConfig {
	config := fan.MockFan.GetConfig()
	config.SanityCheck.FanModeChangedByThirdParty.ThrottleDuration = fan.throttle
	return config
}

func (fan *mockFanWithThrottledModeCheck) GetControlMode() (fans.ControlMode, error) {
	fan.controlModeReads++
	return fan.MockFan.GetControlMode()
}

func TestEnsureFanModeIsSetToExpectedMode_ThrottleUsesClock(t *testing.T) {
	// GIVEN
	fan := &mockFanWithThrottledModeCheck{
		MockFan: MockFan{
			ID:          "fan",
			ControlMode: fans.ControlModeAutomatic,
			sanityCheckFanModeChangedByThirdPartyEnabled: true,
		},
		throttle: 10 * time.Second,
	}
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := DefaultFanController{fan: fan, clock: clock}

	// WHEN: first check
	controller.ensureFanModeIsSetToExpectedMode()

	// THEN
	assert.Equal(t, 1, fan.controlModeReads)
	assert.Equal(t, fans.ControlModePWM, fan.ControlMode)

	// WHEN: a third party changes the mode, and the throttle has not passed yet
	fan.ControlMode = fans.ControlModeAutomatic
	clock.Advance(10*time.Second - time.Millisecond)
	controller.ensureFanModeIsSetToExpectedMode()

	// THEN
	assert.Equal(t, 1, fan.controlModeReads)
	assert.Equal(t, fans.ControlModeAutomatic, fan.ControlMode)

	// WHEN: the throttle has passed
	clock.Advance(time.Millisecond)
	controller.ensureFanModeIsSetToExpectedMode()

	// THEN
	assert.Equal(t, 2, fan.controlModeReads)
	assert.Equal(t, fans.ControlModePWM, fan.ControlMode)
}

func TestComputeSetPwmToGetPwmMapAutomatically_WaitsPwmSetDelayOnClock(t *testing.T) {
	// GIVEN
	pwmSetDelay := 100 * time.Millisecond
	fan := &MockFan{ID: "fan", PwmSetDelay: &pwmSetDelay}
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	controller := DefaultFanController{fan: fan, clock: clock}
	start := clock.Now()

	// WHEN
	err := controller.computeSetPwmToGetPwmMapAutomatically(context.Background())

	// THEN: one pwmSetDelay per PWM value and per write of the readback delay measurement (4), without real sleeps
	assert.NoError(t, err)
	assert.Len(t, controller.setPwmToGetPwmMap, 256)
	assert.Equal(t, (4+256)*pwmSetDelay, clock.Now().Sub(start))
}

func TestComputeSetPwmToGetPwmMapAutomatically_Cancelled(t *testing.T) {
	// GIVEN
	pwmSetDelay := 100 * time.Millisecond
	fan := &mockFanRecordingWrites{MockFan: MockFan{ID: "fan", PwmSetDelay: &pwmSetDelay}}
	controller := DefaultFanController{fan: fan, clock: util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// WHEN
	err := controller.computeSetPwmToGetPwmMapAutomatically(ctx)

	// THEN: the sweep stops after the first write
	assert.True(t, errors.Is(err, context.Canceled))
	assert.Len(t, fan.pwmWrites, 1)
}

// mockFanUnsettledRpm fails every RPM read, so it never settles.
type mockFanUnsettledRpm struct {
	MockFan
	rpmReads int
}

func (fan *mockFanUnsettledRpm) GetRpm() (int, error) {
	fan.rpmReads++
	return 0, errors.New("rpm not readable")
}

func TestWaitForFanToSettle_TimeoutAndSampleIntervalUseClock(t *testing.T) {
	// GIVEN
	fan := &mockFanUnsettledRpm{MockFan: MockFan{ID: "fan"}}
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	controller := &DefaultFanController{fan: fan, clock: clock}
	analyzer := NewFanCurveAnalyzer(controller)
	analyzer.settleSampleInterval = 100 * time.Millisecond
	start := clock.Now()

	// WHEN
	err := analyzer.waitForFanToSettle(context.Background(), fan, 30*time.Second)

	// THEN: gives up after the timeout, sampling every settleSampleInterval
	assert.NoError(t, err)
	elapsed := clock.Now().Sub(start)
	assert.GreaterOrEqual(t, elapsed, 30*time.Second)
	assert.Less(t, elapsed, 30*time.Second+analyzer.settleSampleInterval+time.Millisecond)
	// 1 initial read + one read per interval
	assert.Equal(t, 1+int(elapsed/analyzer.settleSampleInterval), fan.rpmReads)
}

func TestMeasureAtPwm_SubSecondFanResponseDelay(t *testing.T) {
	tests := []struct {
		name             string
		pwmSetDelay      time.Duration
		fanResponseDelay time.Duration
		expectedRpm      float64
	}{
		// reported PWM converges after 400 ms: a 500 ms response delay is enough
		{name: "500ms is enough", pwmSetDelay: time.Millisecond, fanResponseDelay: 500 * time.Millisecond, expectedRpm: 900},
		// 3 attempts with 2 x 100 ms in between are not enough: the target is skipped
		{name: "100ms is too short", pwmSetDelay: time.Millisecond, fanResponseDelay: 100 * time.Millisecond, expectedRpm: -1},
		// the pwmSetDelay before each readback counts too: 150 ms + 100 ms + 150 ms = 400 ms
		{name: "pwmSetDelay counts", pwmSetDelay: 150 * time.Millisecond, fanResponseDelay: 100 * time.Millisecond, expectedRpm: 900},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			originalConfig := configuration.CurrentConfig
			defer func() {
				configuration.CurrentConfig = originalConfig
			}()
			configuration.CurrentConfig.FanController.PwmSetDelay = tt.pwmSetDelay
			configuration.CurrentConfig.FanResponseDelay = configuration.SecondsOrDuration(tt.fanResponseDelay)
			configuration.CurrentConfig.Analysis.SampleCount = 1
			configuration.CurrentConfig.Analysis.SampleDelay = 0

			clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
			fan := &mockFanLaggingPwmReadback{
				MockFan: MockFan{ID: "fan", RPM: 900},
				clock:   clock,
				lag:     400 * time.Millisecond,
			}
			controller := &DefaultFanController{fan: fan, clock: clock}
			for i := 0; i < 256; i++ {
				controller.pwmMapping[i] = i
			}
			analyzer := NewFanCurveAnalyzer(controller)

			// WHEN
			rpm, err := analyzer.measureAtPwm(context.Background(), fan, 128, 0)

			// THEN
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedRpm, rpm)
		})
	}
}
