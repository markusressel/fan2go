package controller

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
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
	dir := t.TempDir()
	s := stallTestFan{
		rpmPath: filepath.Join(dir, "fan1_input"),
		pwmPath: filepath.Join(dir, "pwm1"),
	}
	s.setRpm(t, "0")
	require.NoError(t, os.WriteFile(s.pwmPath, []byte("0"), 0644))
	fan, err := fans.NewFan(configuration.FanConfig{
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
	})
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
