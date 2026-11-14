package controller

import (
	"context"
	"os"
	"path/filepath"
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

// The tests in this file run the actors of a fan controller (RPM monitor, update loop)
// and the statistics collector concurrently, the same way the daemon does. Run them with -race.

func newIdentityMappedController(fan fans.Fan, curveValue float64) *DefaultFanController {
	controller := NewFanController(
		mockPersistence{hasSavedPwmData: true},
		fan,
		MockCurve{ID: "curve", Value: &curveValue},
		control_loop.NewDirectControlLoop(nil),
		time.Millisecond,
		false,
	)
	controller.pwmMapping = createOneToOnePwmMap()
	controller.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
	return controller
}

// A file-backed HwMonFan without a pwm_enable file: the update loop disables the
// fan mode sanity check at runtime, increases the minPwm offset because the fan reports 0 RPM,
// and counts unexpected PWM values, while the RPM monitor reads the fan's RPM input path.
func TestFanController_ConcurrentRpmMonitorUpdateAndStatistics_HwMonFan(t *testing.T) {
	originalConfig := configuration.CurrentConfig
	defer func() {
		configuration.CurrentConfig = originalConfig
	}()
	configuration.CurrentConfig.RpmRollingWindowSize = 10

	// GIVEN
	dir := t.TempDir()
	rpmPath := filepath.Join(dir, "fan1_input")
	pwmPath := filepath.Join(dir, "pwm1")
	require.NoError(t, os.WriteFile(rpmPath, []byte("0"), 0644))
	require.NoError(t, os.WriteFile(pwmPath, []byte("0"), 0644))

	minPwm := 20
	maxPwm := 255
	fan, err := fans.NewFan(configuration.FanConfig{
		ID:        "hwmon",
		NeverStop: true,
		MinPwm:    &minPwm,
		MaxPwm:    &maxPwm,
		HwMon: &configuration.HwMonFanConfig{
			Index:         1,
			RpmInputPath:  rpmPath,
			PwmPath:       pwmPath,
			PwmEnablePath: filepath.Join(dir, "pwm1_enable_missing"),
		},
	})
	require.NoError(t, err)

	controller := newIdentityMappedController(fan, 0)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller.SetClock(clock)

	// WHEN
	const iterations = 100
	// all goroutines start at the same time, so their accesses overlap
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			controller.measureRpm(fan)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			_ = controller.UpdateFanSpeed()
			// simulate a third party changing the PWM value
			_ = os.WriteFile(pwmPath, []byte("7"), 0644)
			// the next update confirms the change
			clock.Advance(minThirdPartyConfirmationDelay)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			_ = controller.GetStatistics()
			_, _ = controller.GetLastSetPwm()
			_, _ = controller.GetRpmAvg()
		}
	}()
	close(start)
	wg.Wait()

	// THEN (no race reported)
	stats := controller.GetStatistics()
	assert.Greater(t, stats.IncreasedMinPwmCount, 0)
	assert.Equal(t, stats.IncreasedMinPwmCount, stats.MinPwmOffset)
	assert.Greater(t, stats.UnexpectedPwmValueCount, 0)
}

// Run drives the RPM monitor and the update loop with the same (fake) clock, so both actors
// access the fan concurrently, while the statistics are read from a third goroutine.
// The fan never spins, so the update loop increases the minPwm offset on every tick
// until the fan stalls at max pwm and Run returns.
func TestFanController_Run_ConcurrentRpmMonitorAndStatistics(t *testing.T) {
	originalConfig := configuration.CurrentConfig
	defer func() {
		configuration.CurrentConfig = originalConfig
	}()
	configuration.CurrentConfig.TempSensorPollingRate = time.Millisecond
	configuration.CurrentConfig.RpmPollingRate = time.Millisecond
	configuration.CurrentConfig.FanController.PwmSetDelay = 0
	configuration.CurrentConfig.RpmRollingWindowSize = 10

	// GIVEN
	fan := &MockFan{
		ID:                     "fan",
		PWM:                    20,
		MinPWM:                 20,
		MaxPWM:                 30,
		RPM:                    0,
		shouldNeverStop:        true,
		useUnscaledCurveValues: true,
		speedCurve:             &map[int]float64{100: 0},
	}
	controller := newIdentityMappedController(fan, 0)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller.SetClock(clock)

	done := make(chan error, 1)
	go func() {
		done <- controller.Run(context.Background())
	}()

	// WHEN
	advanceThroughRunStartup(t, clock)
	deadline := time.After(10 * time.Second)
	for running := true; running; {
		select {
		case err := <-done:
			assert.NoError(t, err)
			running = false
		case <-deadline:
			t.Fatal("Run() did not return after the fan stalled at max pwm")
		default:
			_ = controller.GetStatistics()
			_, _ = controller.GetLastSetPwm()
			_, _ = controller.GetRpmAvg()
			clock.Advance(time.Millisecond)
		}
	}

	// THEN (no race reported)
	stats := controller.GetStatistics()
	assert.Equal(t, 10, stats.IncreasedMinPwmCount)
	assert.Equal(t, 10, stats.MinPwmOffset)
}

// mockFanWithoutReadback can't read back its PWM value or control mode,
// and counts how often the control mode read support is queried.
type mockFanWithoutReadback struct {
	MockFan
	controlModeReadQueries int
}

func (fan *mockFanWithoutReadback) Supports(feature fans.FeatureFlag) bool {
	switch feature {
	case fans.FeatureControlModeRead:
		fan.controlModeReadQueries++
		return false
	case fans.FeaturePwmSensor:
		return false
	}
	return true
}

func (fan *mockFanWithoutReadback) SetConfig(config configuration.FanConfig) {
	panic("the controller must not replace the fan config")
}

// When a fan can't read back its PWM value or control mode, the controller disables the
// corresponding sanity checks at runtime. The fan config shows them as runtime overrides
// (e.g. in the REST API), and they aren't evaluated again.
func TestFanController_SanityChecksDisabledAtRuntime(t *testing.T) {
	// GIVEN
	fan := &mockFanWithoutReadback{MockFan: MockFan{
		ID: "fan",
		sanityCheckFanModeChangedByThirdPartyEnabled: true,
	}}
	controller := newIdentityMappedController(fan, 100)
	lastTarget := 100
	controller.lastTarget = &lastTarget

	// WHEN
	for i := 0; i < 3; i++ {
		assert.NoError(t, controller.UpdateFanSpeed())
	}

	// THEN
	disabled := configuration.Optional[bool]{Value: false, Present: true, RuntimeOverride: true}
	sanityCheck := fan.GetConfig().SanityCheck
	assert.Equal(t, disabled, sanityCheck.PwmValueChangedByThirdParty.Enabled.Optional)
	assert.Equal(t, disabled, sanityCheck.FanModeChangedByThirdParty.Enabled.Optional)
	// each check was disabled once, during the first update, and isn't evaluated again
	assert.Equal(t, []fans.SanityCheck{fans.SanityCheckPwmValueChangedByThirdParty, fans.SanityCheckFanModeChangedByThirdParty}, fan.disabledSanityChecks)
	assert.Equal(t, 1, fan.controlModeReadQueries)
}
