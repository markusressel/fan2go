package controller

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/controller/status"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/quirks"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file drive the RPM monitor (measureRpm) and the update loop (UpdateFanSpeed)
// of a fan that should never stop by hand, with a fake clock, the way Run interleaves them.

const stallTestRpmPollingRate = time.Second

// stallTestFan is a file-backed fan whose RPM value can be changed by the test.
type stallTestFan struct {
	fan     fans.Fan
	rpmPath string
	pwmPath string
}

func (s stallTestFan) setRpm(t *testing.T, rpm string) {
	t.Helper()
	require.NoError(t, os.WriteFile(s.rpmPath, []byte(rpm), 0644))
}

func (s stallTestFan) pwm(t *testing.T) int {
	t.Helper()
	pwm, err := util.ReadIntFromFile(s.pwmPath)
	require.NoError(t, err)
	return pwm
}

func newStallTestHwMonFan(t *testing.T, minPwm int, maxPwm int) stallTestFan {
	t.Helper()
	return newStallTestHwMonFanWithConfig(t, minPwm, maxPwm, nil)
}

// newStallTestHwMonFanWithConfig creates a file-backed hwmon fan with neverStop, whose config can be changed
// by modify (if not nil) before the fan is created.
func newStallTestHwMonFanWithConfig(t *testing.T, minPwm int, maxPwm int, modify func(config *configuration.FanConfig)) stallTestFan {
	t.Helper()
	dir := t.TempDir()
	s := stallTestFan{
		rpmPath: filepath.Join(dir, "fan1_input"),
		pwmPath: filepath.Join(dir, "pwm1"),
	}
	s.setRpm(t, "0")
	require.NoError(t, os.WriteFile(s.pwmPath, []byte("0"), 0644))
	config := configuration.FanConfig{
		ID:        "hwmon",
		NeverStop: true,
		MinPwm:    &minPwm,
		MaxPwm:    &maxPwm,
		HwMon: &configuration.HwMonFanConfig{
			Index:         1,
			RpmInputPath:  s.rpmPath,
			PwmPath:       s.pwmPath,
			PwmEnablePath: filepath.Join(dir, "pwm1_enable_missing"),
		},
	}
	if modify != nil {
		modify(&config)
	}
	fan, err := fans.NewFan(config)
	require.NoError(t, err)
	s.fan = fan
	return s
}

func newStallTestFileFan(t *testing.T) stallTestFan {
	t.Helper()
	dir := t.TempDir()
	s := stallTestFan{
		rpmPath: filepath.Join(dir, "rpm"),
		pwmPath: filepath.Join(dir, "pwm"),
	}
	s.setRpm(t, "0")
	require.NoError(t, os.WriteFile(s.pwmPath, []byte("0"), 0644))
	fan, err := fans.NewFan(configuration.FanConfig{
		ID:        "file",
		NeverStop: true,
		File: &configuration.FileFanConfig{
			Path:    s.pwmPath,
			RpmPath: s.rpmPath,
		},
	})
	require.NoError(t, err)
	s.fan = fan
	return s
}

func setupStallTestConfig(t *testing.T) {
	t.Helper()
	originalConfig := configuration.CurrentConfig
	t.Cleanup(func() {
		configuration.CurrentConfig = originalConfig
	})
	configuration.CurrentConfig.RpmRollingWindowSize = 10
	configuration.CurrentConfig.RpmPollingRate = stallTestRpmPollingRate
}

func newStallTestController(t *testing.T, fan fans.Fan) (*DefaultFanController, *util.FakeClock) {
	t.Helper()
	controller := newIdentityMappedController(fan, 0)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller.SetClock(clock)
	return controller, clock
}

// pollAndUpdate simulates one RPM polling period: the RPM monitor reads the fan, then the update loop runs.
func pollAndUpdate(t *testing.T, controller *DefaultFanController, clock *util.FakeClock) error {
	t.Helper()
	clock.Advance(stallTestRpmPollingRate)
	controller.measureRpm(controller.fan)
	return controller.UpdateFanSpeed()
}

func pollAndUpdateN(t *testing.T, controller *DefaultFanController, clock *util.FakeClock, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		require.NoError(t, pollAndUpdate(t, controller, clock))
	}
}

// A running fan stops: once the RPM average drops below the spin threshold, the minPwm offset is increased.
// Same behaviour for all fan types: the average is kept separately from the raw RPM value.
func TestFanController_NeverStop_StalledFanIncreasesMinPwm(t *testing.T) {
	tests := []struct {
		name   string
		newFan func(t *testing.T) stallTestFan
		minPwm int
	}{
		{"hwmon", func(t *testing.T) stallTestFan { return newStallTestHwMonFan(t, 20, 255) }, 20},
		{"file", newStallTestFileFan, fans.MinPwmValue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			setupStallTestConfig(t)
			s := tt.newFan(t)
			controller, clock := newStallTestController(t, s.fan)
			s.setRpm(t, "1000")
			pollAndUpdateN(t, controller, clock, 20)
			assert.Equal(t, 1000.0, s.fan.GetRpmAvg())
			assert.Equal(t, tt.minPwm, s.pwm(t))

			// WHEN the fan stops
			s.setRpm(t, "0")
			// 1000 * 0.9^28 = 52.3 RPM: still above the spin threshold (50 RPM)
			pollAndUpdateN(t, controller, clock, 28)
			assert.Equal(t, 0, controller.GetStatistics().IncreasedMinPwmCount)
			// 1000 * 0.9^29 = 47.1 RPM: below the spin threshold
			pollAndUpdateN(t, controller, clock, 1)

			// THEN
			stats := controller.GetStatistics()
			assert.Equal(t, 1, stats.IncreasedMinPwmCount)
			assert.Equal(t, 1, stats.MinPwmOffset)
			assert.Equal(t, tt.minPwm+1, s.pwm(t))
		})
	}
}

// After an increase, the fan gets a few RPM polling periods to react before the next one.
func TestFanController_NeverStop_MinPwmIncreasesAreRateLimited(t *testing.T) {
	// GIVEN a fan that doesn't spin
	setupStallTestConfig(t)
	s := newStallTestHwMonFan(t, 20, 255)
	controller, clock := newStallTestController(t, s.fan)
	// the first update sets minPwm, the stall is detected once the fan had time to spin up
	pollAndUpdateN(t, controller, clock, 1+minPwmIncreaseRpmPollingPeriods)
	assert.Equal(t, 1, controller.GetStatistics().IncreasedMinPwmCount)

	// WHEN the update loop runs more often than the RPM monitor
	for i := 0; i < 10; i++ {
		require.NoError(t, controller.UpdateFanSpeed())
	}
	// THEN
	assert.Equal(t, 1, controller.GetStatistics().IncreasedMinPwmCount)

	// WHEN less than minPwmIncreaseRpmPollingPeriods polling periods have passed
	pollAndUpdateN(t, controller, clock, minPwmIncreaseRpmPollingPeriods-1)
	// THEN
	assert.Equal(t, 1, controller.GetStatistics().IncreasedMinPwmCount)
	assert.Equal(t, 21, s.pwm(t))

	// WHEN minPwmIncreaseRpmPollingPeriods polling periods have passed
	pollAndUpdateN(t, controller, clock, 1)
	// THEN
	assert.Equal(t, 2, controller.GetStatistics().IncreasedMinPwmCount)
	assert.Equal(t, 22, s.pwm(t))

	// and so on
	pollAndUpdateN(t, controller, clock, 2*minPwmIncreaseRpmPollingPeriods)
	assert.Equal(t, 4, controller.GetStatistics().IncreasedMinPwmCount)
	assert.Equal(t, 24, s.pwm(t))
}

// A fan that doesn't spin even at maxPwm stops the controller (after it had time to react to maxPwm).
func TestFanController_NeverStop_StalledAtMaxPwm(t *testing.T) {
	// GIVEN a fan that doesn't spin
	setupStallTestConfig(t)
	s := newStallTestHwMonFan(t, 20, 22)
	controller, clock := newStallTestController(t, s.fan)
	pollAndUpdateN(t, controller, clock, 1+minPwmIncreaseRpmPollingPeriods)
	pollAndUpdateN(t, controller, clock, minPwmIncreaseRpmPollingPeriods)
	assert.Equal(t, 2, controller.GetStatistics().IncreasedMinPwmCount)
	assert.Equal(t, 22, s.pwm(t))

	// WHEN the fan had less than minPwmIncreaseRpmPollingPeriods polling periods to react to maxPwm
	pollAndUpdateN(t, controller, clock, minPwmIncreaseRpmPollingPeriods-1)

	// THEN
	err := pollAndUpdate(t, controller, clock)
	assert.ErrorIs(t, err, ErrFanStalledAtMaxPwm)
	assert.Equal(t, 2, controller.GetStatistics().IncreasedMinPwmCount)
}

// A fan at standstill before fan2go takes control (e.g. a zero RPM mode) gets time to spin up at minPwm.
func TestFanController_NeverStop_FanStoppedAtStartGetsTimeToSpinUp(t *testing.T) {
	// GIVEN a fan at standstill, the first reading seeds the RPM average with 0
	setupStallTestConfig(t)
	s := newStallTestHwMonFan(t, 20, 255)
	controller, clock := newStallTestController(t, s.fan)
	controller.measureRpm(s.fan)
	require.NoError(t, controller.UpdateFanSpeed())
	assert.Equal(t, 20, s.pwm(t))

	// WHEN the fan spins up within a few RPM polling periods
	pollAndUpdateN(t, controller, clock, 1)
	s.setRpm(t, "300")
	// 0 -> 30 -> 57 RPM average
	pollAndUpdateN(t, controller, clock, 2)
	assert.Equal(t, 57.0, s.fan.GetRpmAvg())
	pollAndUpdateN(t, controller, clock, 20)

	// THEN
	assert.Equal(t, 0, controller.GetStatistics().IncreasedMinPwmCount)
	assert.Equal(t, 20, s.pwm(t))
}

// A failed RPM reading isn't a 0 RPM reading.
func TestFanController_NeverStop_RpmReadErrorsDontCauseAnIncrease(t *testing.T) {
	// GIVEN
	setupStallTestConfig(t)
	s := newStallTestHwMonFan(t, 20, 255)
	controller, clock := newStallTestController(t, s.fan)
	s.setRpm(t, "1000")
	pollAndUpdateN(t, controller, clock, 5)

	// WHEN reading the RPM value fails, once and repeatedly
	s.setRpm(t, "not a number")
	pollAndUpdateN(t, controller, clock, 100)

	// THEN
	assert.Equal(t, 1000.0, s.fan.GetRpmAvg())
	assert.Equal(t, 0, controller.GetStatistics().IncreasedMinPwmCount)
	assert.Equal(t, 20, s.pwm(t))
}

// The RPM average starts with the first successful reading, and stall detection waits for it.
func TestFanController_NeverStop_RpmAvgIsSeededByFirstReading(t *testing.T) {
	// GIVEN a running fan, whose RPM average is 0 since it hasn't been measured yet
	setupStallTestConfig(t)
	s := newStallTestHwMonFan(t, 20, 255)
	s.setRpm(t, "1000")
	controller, clock := newStallTestController(t, s.fan)
	assert.Equal(t, 0.0, s.fan.GetRpmAvg())

	// WHEN the update loop runs before the RPM monitor
	for i := 0; i < 5; i++ {
		clock.Advance(stallTestRpmPollingRate)
		require.NoError(t, controller.UpdateFanSpeed())
	}
	// THEN
	assert.Equal(t, 0, controller.GetStatistics().IncreasedMinPwmCount)

	// WHEN the first reading fails
	s.setRpm(t, "not a number")
	controller.measureRpm(s.fan)
	require.NoError(t, controller.UpdateFanSpeed())
	// THEN
	assert.Equal(t, 0.0, s.fan.GetRpmAvg())
	assert.Equal(t, 0, controller.GetStatistics().IncreasedMinPwmCount)

	// WHEN the first successful reading arrives
	s.setRpm(t, "1000")
	controller.measureRpm(s.fan)
	// THEN it replaces the average instead of being blended into it
	assert.Equal(t, 1000.0, s.fan.GetRpmAvg())
	require.NoError(t, controller.UpdateFanSpeed())
	assert.Equal(t, 0, controller.GetStatistics().IncreasedMinPwmCount)

	// WHEN the next readings arrive
	s.setRpm(t, "0")
	controller.measureRpm(s.fan)
	// THEN they are blended into the average
	assert.Equal(t, 900.0, s.fan.GetRpmAvg())
}

// The RPM monitor seeds the RPM average while the update loop checks whether it has been seeded.
// Run with -race: each iteration uses a fresh controller, so the first measurement and an update overlap.
func TestFanController_NeverStop_RpmAvgSeedingIsSynchronized(t *testing.T) {
	setupStallTestConfig(t)
	for i := 0; i < 200; i++ {
		fan := &MockFan{
			ID:                     "fan",
			MinPWM:                 20,
			MaxPWM:                 255,
			RPM:                    0,
			shouldNeverStop:        true,
			useUnscaledCurveValues: true,
		}
		controller := newIdentityMappedController(fan, 0)
		lastTarget := 20
		controller.lastTarget = &lastTarget

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			controller.measureRpm(fan)
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = controller.UpdateFanSpeed()
		}()
		close(start)
		wg.Wait()
	}
}

// The stall check of fans without neverStop: warn and notify once when the fan doesn't spin although its target
// is at least its minPwm, log the recovery, and change neither the PWM value nor the state.

const stallCheckTestMinPwm = 20

// stallCheckTest is a hwmon fan without neverStop (minPwm 20, maxPwm 255) whose curve value can be changed,
// with a controller that records its notifications.
type stallCheckTest struct {
	stallTestFan
	controller    *DefaultFanController
	clock         *util.FakeClock
	curveValue    *float64
	notifications []string
}

func newStallCheckTest(t *testing.T, modify func(config *configuration.FanConfig)) *stallCheckTest {
	t.Helper()
	setupStallTestConfig(t)
	s := newStallTestHwMonFanWithConfig(t, stallCheckTestMinPwm, 255, func(config *configuration.FanConfig) {
		config.NeverStop = false
		if modify != nil {
			modify(config)
		}
	})
	return newStallCheckTestForFan(t, s, s.fan)
}

func newStallCheckTestForFan(t *testing.T, s stallTestFan, fan fans.Fan) *stallCheckTest {
	t.Helper()
	curveValue := 255.0
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
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller.SetClock(clock)
	result := &stallCheckTest{stallTestFan: s, controller: controller, clock: clock, curveValue: &curveValue}
	controller.errorNotifier = func(title, format string, a ...any) {
		result.notifications = append(result.notifications, title+": "+fmt.Sprintf(format, a...))
	}
	return result
}

func (s *stallCheckTest) pollAndUpdateN(t *testing.T, n int) {
	t.Helper()
	pollAndUpdateN(t, s.controller, s.clock, n)
}

// spinThenStop lets the fan spin at maxPwm (so its tach is credible), then stops it until the average is
// just above the spin threshold: the next poll makes it a stall.
func (s *stallCheckTest) spinThenStop(t *testing.T) {
	t.Helper()
	s.setRpm(t, "1000")
	s.pollAndUpdateN(t, 10)
	s.setRpm(t, "0")
	// 1000 * 0.9^28 = 52.3 RPM: still above the spin threshold (50 RPM)
	s.pollAndUpdateN(t, 28)
	require.Empty(t, s.notifications)
}

func TestFanController_StallCheck_StallIsReportedOnceAndRecoveryIsLogged(t *testing.T) {
	// GIVEN a spinning fan that stops at maxPwm
	s := newStallCheckTest(t, nil)
	s.spinThenStop(t)

	// WHEN the average drops below the spin threshold, and the fan stays stopped
	s.pollAndUpdateN(t, 1)
	s.pollAndUpdateN(t, 50)

	// THEN the stall is reported once, and nothing else changes
	require.Len(t, s.notifications, 1)
	assert.Contains(t, s.notifications[0], "Fan Stalled: Fan hwmon doesn't spin: its avg. RPM is 47 at PWM 255 (minPwm 20)")
	assert.Equal(t, 1, s.controller.GetStatistics().StallCount)
	assert.Equal(t, 0, s.controller.GetStatistics().IncreasedMinPwmCount)
	assert.Equal(t, 255, s.pwm(t))
	assert.Equal(t, status.StateRunning, s.controller.GetStatus().State)

	// WHEN the fan spins again
	s.setRpm(t, "1000")
	recoveryLog := captureLog(t, func() {
		s.pollAndUpdateN(t, 1)
	})
	// THEN the recovery is logged
	assert.Contains(t, recoveryLog, "Fan hwmon is spinning again")

	// WHEN it stalls again: a new stall is reported
	s.setRpm(t, "0")
	s.pollAndUpdateN(t, 40)
	assert.Len(t, s.notifications, 2)
	assert.Equal(t, 2, s.controller.GetStatistics().StallCount)
}

func TestFanController_StallCheck_NoReportForATachNeverSeenSpinning(t *testing.T) {
	// GIVEN a fan whose RPM input always reads 0 (e.g. no tach connected) and that has no curve data
	s := newStallCheckTest(t, nil)

	// WHEN
	s.pollAndUpdateN(t, 50)

	// THEN
	assert.Empty(t, s.notifications)
	assert.Equal(t, 0, s.controller.GetStatistics().StallCount)

	// WHEN the curve data shows that the tach works
	require.NoError(t, s.fan.AttachFanRpmCurveData(&map[int]float64{0: 0, 20: 300, 255: 1500}))
	s.pollAndUpdateN(t, 1)

	// THEN
	assert.Len(t, s.notifications, 1)
}

func TestFanController_StallCheck_NoCheckWhileTargetIsZero(t *testing.T) {
	// GIVEN a fan whose curve stops it
	s := newStallCheckTest(t, nil)
	s.spinThenStop(t)
	*s.curveValue = 0

	// WHEN
	s.pollAndUpdateN(t, 50)

	// THEN
	assert.Equal(t, 0, s.pwm(t))
	assert.Empty(t, s.notifications)
}

func TestFanController_StallCheck_FanGetsTimeToReactToANewTarget(t *testing.T) {
	// GIVEN a fan that stopped (target 0) and whose RPM average is 0
	s := newStallCheckTest(t, nil)
	s.setRpm(t, "1000")
	s.pollAndUpdateN(t, 5)
	*s.curveValue = 0
	s.setRpm(t, "0")
	s.pollAndUpdateN(t, 60)
	require.Empty(t, s.notifications)

	// WHEN the target alternates at minPwm: the fan gets time to react to every change
	for i := 0; i < 20; i++ {
		*s.curveValue = float64(1 + i%2)
		s.pollAndUpdateN(t, minPwmIncreaseRpmPollingPeriods-1)
	}
	// THEN
	assert.Empty(t, s.notifications)

	// WHEN the target stays at minPwm, until the fan had time to react
	*s.curveValue = 1
	s.pollAndUpdateN(t, minPwmIncreaseRpmPollingPeriods)
	assert.Empty(t, s.notifications)
	s.pollAndUpdateN(t, 1)

	// THEN
	require.Len(t, s.notifications, 1)
	assert.Contains(t, s.notifications[0], "at PWM 20 (minPwm 20)")
}

func TestFanController_StallCheck_TargetChangesAboveMinPwmDontRestartTheReactionTime(t *testing.T) {
	// GIVEN a fan that stopped at maxPwm
	s := newStallCheckTest(t, nil)
	s.spinThenStop(t)

	// WHEN the target changes above minPwm on every update
	for i := 0; i < 2; i++ {
		*s.curveValue = float64(200 + i)
		s.pollAndUpdateN(t, 1)
	}
	// THEN the stall is reported nevertheless
	assert.Len(t, s.notifications, 1)
}

func TestFanController_StallCheck_SlowSpinUpIsNotAStall(t *testing.T) {
	// GIVEN a fan at standstill with a credible tach
	s := newStallCheckTest(t, nil)
	s.setRpm(t, "1000")
	s.pollAndUpdateN(t, 5)
	*s.curveValue = 0
	s.setRpm(t, "0")
	s.pollAndUpdateN(t, 60)

	// WHEN it spins up slowly at minPwm: the average stays below 50 RPM after the reaction time
	*s.curveValue = 1
	s.pollAndUpdateN(t, 1)
	s.setRpm(t, "100")
	// 0 -> 10 -> 19 -> 27.1 RPM average
	s.pollAndUpdateN(t, 3)
	assert.Less(t, s.fan.GetRpmAvg(), 50.0)
	s.pollAndUpdateN(t, 10)

	// THEN its last reading shows it spinning, so it is not reported
	assert.Empty(t, s.notifications)
}

func TestFanController_StallCheck_PwmMapToZeroIsNotChecked(t *testing.T) {
	// GIVEN a fan whose pwmMap maps its targets to 0
	s := newStallCheckTest(t, nil)
	s.spinThenStop(t)
	for i := range s.controller.pwmMapping {
		s.controller.pwmMapping[i] = 0
	}

	// WHEN
	s.pollAndUpdateN(t, 50)

	// THEN
	assert.Empty(t, s.notifications)
}

func TestFanController_StallCheck_NeverStopFansUseTheirOwnStallHandling(t *testing.T) {
	// GIVEN a fan with neverStop (and the stall check enabled explicitly)
	setupStallTestConfig(t)
	s := newStallTestHwMonFanWithConfig(t, 20, 255, func(config *configuration.FanConfig) {
		config.SanityCheck.FanStalled.Enabled.SetOverride(true)
	})
	test := newStallCheckTestForFan(t, s, s.fan)
	*test.curveValue = 0
	test.spinThenStop(t)

	// WHEN it stalls
	test.pollAndUpdateN(t, 1)

	// THEN only the minPwm offset is increased, as before, and nothing is notified
	assert.Equal(t, 1, test.controller.GetStatistics().IncreasedMinPwmCount)
	assert.Equal(t, 21, test.pwm(t))
	assert.Equal(t, 0, test.controller.GetStatistics().StallCount)
	assert.Empty(t, test.notifications)
}

// quirkyFan adds a known hardware quirks entry to a fan.
type quirkyFan struct {
	fans.Fan
	entry *quirks.Entry
}

func (f quirkyFan) KnownQuirks() *quirks.Entry {
	return f.entry
}

func TestFanController_StallCheck_ConfigAndKnownQuirks(t *testing.T) {
	firmwareMayStopFan := &quirks.Entry{ID: "test", Version: 1, FirmwareMayStopFan: true}
	tests := []struct {
		name       string
		enabled    *bool
		entry      *quirks.Entry
		wantReport bool
	}{
		{"default", nil, nil, true},
		{"disabled", boolPtr(false), nil, false},
		{"enabled", boolPtr(true), nil, true},
		{"entry without FirmwareMayStopFan", nil, &quirks.Entry{ID: "other", Version: 1}, true},
		{"FirmwareMayStopFan: off by default", nil, firmwareMayStopFan, false},
		{"FirmwareMayStopFan: config enables it", boolPtr(true), firmwareMayStopFan, true},
		{"FirmwareMayStopFan: config disables it", boolPtr(false), firmwareMayStopFan, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			setupStallTestConfig(t)
			s := newStallTestHwMonFanWithConfig(t, stallCheckTestMinPwm, 255, func(config *configuration.FanConfig) {
				config.NeverStop = false
				if tt.enabled != nil {
					config.SanityCheck.FanStalled.Enabled = configuration.DefaultTrueBool{
						Optional: configuration.Optional[bool]{Value: *tt.enabled, Present: true},
					}
				}
			})
			fan := s.fan
			if tt.entry != nil {
				fan = quirkyFan{Fan: s.fan, entry: tt.entry}
			}
			test := newStallCheckTestForFan(t, s, fan)

			// WHEN the fan stalls
			test.spinThenStop(t)
			test.pollAndUpdateN(t, 5)

			// THEN
			if tt.wantReport {
				assert.Len(t, test.notifications, 1)
			} else {
				assert.Empty(t, test.notifications)
			}
		})
	}
}

// Fans without an RPM sensor are never checked.
func TestFanController_StallCheck_FanWithoutRpmSensorIsNotChecked(t *testing.T) {
	setupStallTestConfig(t)
	dir := t.TempDir()
	pwmPath := filepath.Join(dir, "pwm")
	require.NoError(t, os.WriteFile(pwmPath, []byte("0"), 0644))
	fan, err := fans.NewFan(configuration.FanConfig{ID: "file", File: &configuration.FileFanConfig{Path: pwmPath}})
	require.NoError(t, err)
	test := newStallCheckTestForFan(t, stallTestFan{fan: fan, pwmPath: pwmPath}, fan)
	require.NoError(t, fan.AttachFanRpmCurveData(&map[int]float64{0: 0, 255: 1500}))

	for i := 0; i < 50; i++ {
		test.clock.Advance(stallTestRpmPollingRate)
		require.NoError(t, test.controller.UpdateFanSpeed())
	}

	assert.Empty(t, test.notifications)
}

func boolPtr(b bool) *bool {
	return &b
}

// A new Run starts over: a lasting stall is reported again, and the tach has to be seen working again.
func TestFanController_StallCheck_ResetRuntimeState(t *testing.T) {
	// GIVEN a reported stall
	s := newStallCheckTest(t, nil)
	s.spinThenStop(t)
	s.pollAndUpdateN(t, 1)
	require.Len(t, s.notifications, 1)

	// WHEN the controller runs again (e.g. after a reload), and the fan still doesn't spin
	s.controller.resetRuntimeState()
	s.pollAndUpdateN(t, 20)
	// THEN the tach wasn't seen working in this run (and there is no curve data): no report
	assert.Len(t, s.notifications, 1)
	assert.False(t, s.controller.rpmSpinSeen.Load())

	// WHEN the curve data shows that the tach works: the stall is reported again
	require.NoError(t, s.fan.AttachFanRpmCurveData(&map[int]float64{0: 0, 20: 300, 255: 1500}))
	s.pollAndUpdateN(t, 1)
	assert.Len(t, s.notifications, 2)
}

// After a curve failure, the fan gets the full reaction period again (it may have been handed back and stopped).
func TestFanController_StallCheck_CurveFailureRestartsTheReactionPeriod(t *testing.T) {
	// GIVEN a fan whose RPM average is just above the threshold
	s := newStallCheckTest(t, nil)
	s.spinThenStop(t)

	// WHEN the curve fails, then recovers while the fan is still stopped
	failing := s.controller.getCurve().(MockCurve)
	failing.Err = errors.New("sensor stale")
	s.controller.UpdateCurve(failing)
	s.clock.Advance(stallTestRpmPollingRate)
	s.controller.measureRpm(s.fan)
	require.Error(t, s.controller.UpdateFanSpeed())
	failing.Err = nil
	s.controller.UpdateCurve(failing)
	// only the stall notifications count here
	s.notifications = nil

	// THEN it isn't reported before it had time to react: the period starts with the first update after the failure
	s.pollAndUpdateN(t, minPwmIncreaseRpmPollingPeriods)
	assert.Empty(t, s.notifications)
	s.pollAndUpdateN(t, 1)
	assert.Len(t, s.notifications, 1)
}

// A third party that changes the PWM value may have stopped the fan: it gets the full reaction period again.
func TestFanController_StallCheck_ThirdPartyPwmChangeRestartsTheReactionPeriod(t *testing.T) {
	// GIVEN a fan whose RPM average is just above the threshold
	s := newStallCheckTest(t, nil)
	s.spinThenStop(t)

	// WHEN a third party sets PWM 0
	require.NoError(t, os.WriteFile(s.pwmPath, []byte("0"), 0644))
	s.pollAndUpdateN(t, 1)

	// THEN
	assert.Empty(t, s.notifications)
	s.pollAndUpdateN(t, minPwmIncreaseRpmPollingPeriods-1)
	assert.Empty(t, s.notifications)
	s.pollAndUpdateN(t, 1)
	assert.Len(t, s.notifications, 1)
}

// A target that couldn't be written isn't checked, and restarts the reaction period.
func TestFanController_StallCheck_FailedWriteIsNotChecked(t *testing.T) {
	// GIVEN a fan whose RPM average is just above the threshold
	s := newStallCheckTest(t, nil)
	s.spinThenStop(t)
	s.controller.errorNotifier = func(title, format string, a ...any) {
		s.notifications = append(s.notifications, title)
	}

	// WHEN writing the PWM value fails (the pwm file is a directory now)
	require.NoError(t, os.Remove(s.pwmPath))
	require.NoError(t, os.Mkdir(s.pwmPath, 0755))
	for i := 0; i < 5; i++ {
		s.clock.Advance(stallTestRpmPollingRate)
		s.controller.measureRpm(s.fan)
		_ = s.controller.UpdateFanSpeed()
	}

	// THEN
	assert.Empty(t, s.notifications)
	assert.True(t, s.controller.stallCheckSince.IsZero())
}

// A failing RPM sensor freezes the average and the last reading: no report while it fails.
func TestFanController_StallCheck_NoReportWhileReadingTheRpmFails(t *testing.T) {
	// GIVEN a stopped fan (target 0) with a credible tach
	s := newStallCheckTest(t, nil)
	s.setRpm(t, "1000")
	s.pollAndUpdateN(t, 5)
	*s.curveValue = 0
	s.setRpm(t, "0")
	s.pollAndUpdateN(t, 60)

	// WHEN reading the RPM fails, and the target rises
	s.setRpm(t, "not a number")
	*s.curveValue = 255
	s.pollAndUpdateN(t, 10)

	// THEN
	assert.Empty(t, s.notifications)

	// WHEN reading works again and shows a stopped fan
	s.setRpm(t, "0")
	s.pollAndUpdateN(t, 1)
	assert.Len(t, s.notifications, 1)
}

// With useUnscaledCurveValues, a curve value below minPwm becomes 0 and isn't checked, one at minPwm is.
func TestFanController_StallCheck_UnscaledCurveValues(t *testing.T) {
	// GIVEN
	s := newStallCheckTest(t, func(config *configuration.FanConfig) {
		config.UseUnscaledCurveValues = true
	})
	s.spinThenStop(t)

	// WHEN the curve value is below minPwm
	*s.curveValue = stallCheckTestMinPwm - 1
	s.pollAndUpdateN(t, 20)
	// THEN
	assert.Equal(t, 0, s.pwm(t))
	assert.Empty(t, s.notifications)

	// WHEN it is at minPwm
	*s.curveValue = stallCheckTestMinPwm
	s.pollAndUpdateN(t, minPwmIncreaseRpmPollingPeriods+1)
	// THEN
	assert.Equal(t, stallCheckTestMinPwm, s.pwm(t))
	assert.Len(t, s.notifications, 1)
}

// With minPwm 0, the floor is 1: a change from 1 restarts the reaction period.
func TestFanController_StallCheck_FloorIsAtLeastOne(t *testing.T) {
	// GIVEN a stopped fan with minPwm 0 and target 1, that had time to react
	s := newStallCheckTest(t, func(config *configuration.FanConfig) {
		zero := 0
		config.MinPwm = &zero
		config.UseUnscaledCurveValues = true
	})
	s.setRpm(t, "1000")
	s.pollAndUpdateN(t, 5)
	*s.curveValue = 0
	s.setRpm(t, "0")
	s.pollAndUpdateN(t, 60)
	*s.curveValue = 1
	s.pollAndUpdateN(t, minPwmIncreaseRpmPollingPeriods-1)

	// WHEN the target changes from 1 to 5
	*s.curveValue = 5
	s.pollAndUpdateN(t, minPwmIncreaseRpmPollingPeriods)
	// THEN the reaction period started again with the change (without the restart, it would be reported by now)
	assert.Empty(t, s.notifications)
	s.pollAndUpdateN(t, 1)
	assert.Len(t, s.notifications, 1)
}
