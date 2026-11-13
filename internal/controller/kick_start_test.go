package controller

import (
	"fmt"
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

// kickTestFan is an analyzed hwmon-like fan with static friction: standing still, it only starts at breakaway
// (raw PWM) or more, after spinUpDelay; spinning, it keeps spinning down to keepSpinning. A fan that stops coasts
// for coastDown (its RPM decays linearly), and a coasting fan that gets keepSpinning or more spins again. Its tach
// reports a new value only every tachInterval (0: every read), like a driver that caches it.
type kickTestFan struct {
	MockFan
	breakaway    int
	keepSpinning int
	startPwm     int
	noRpmSensor  bool
	kickStart    *configuration.KickStartConfig
	// needsKickAtLeast: a fan that was started with less than this stalls again when it gets less than breakaway
	// (it broke free, but didn't get up to speed)
	needsKickAtLeast int

	fanMu        sync.Mutex
	clock        *util.FakeClock
	spinUpDelay  time.Duration
	coastDown    time.Duration
	tachInterval time.Duration
	readError    bool
	// glitches are returned by the next reads, one each, instead of the real value
	glitches []int

	spinning     bool
	startAt      time.Time
	coastFrom    time.Time
	coastRpm     float64
	maxSinceSpin int
	cachedRpm    int
	cachedAt     time.Time
	cacheValid   bool
	rpmAvg       float64
	writes       []int
}

func newKickTestFan(breakaway int, keepSpinning int) *kickTestFan {
	fan := &kickTestFan{
		MockFan:      MockFan{ID: "fan", MinPWM: keepSpinning, ControlMode: fans.ControlModePWM},
		breakaway:    breakaway,
		keepSpinning: keepSpinning,
		startPwm:     keepSpinning,
	}
	// measured by the analysis: the tach works
	fan.speedCurve = &map[int]float64{0: 0, keepSpinning: 200, 255: 840}
	return fan
}

func (fan *kickTestFan) now() time.Time {
	return fan.clock.Now()
}

// physicalRpm returns the RPM of the fan now (fanMu must be held).
func (fan *kickTestFan) physicalRpm(now time.Time) int {
	if !fan.startAt.IsZero() && !now.Before(fan.startAt) {
		fan.spinning = true
		fan.startAt = time.Time{}
		fan.coastFrom = time.Time{}
	}
	if fan.spinning {
		return 100 + 3*fan.PWM
	}
	if !fan.coastFrom.IsZero() && now.Sub(fan.coastFrom) < fan.coastDown {
		return int(fan.coastRpm * (1 - float64(now.Sub(fan.coastFrom))/float64(fan.coastDown)))
	}
	return 0
}

func (fan *kickTestFan) SetPwm(pwm int) error {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	if fan.setPwmAlwaysFails {
		return fmt.Errorf("write failed")
	}
	now := fan.now()
	rpm := fan.physicalRpm(now)
	fan.PWM = pwm
	fan.writes = append(fan.writes, pwm)
	switch {
	case fan.spinning && (pwm < fan.keepSpinning || (pwm < fan.breakaway && fan.maxSinceSpin < fan.needsKickAtLeast)):
		fan.spinning = false
		fan.coastFrom = now
		fan.coastRpm = float64(rpm)
	case !fan.spinning && rpm > 0 && pwm >= fan.keepSpinning:
		// caught while coasting
		fan.spinning = true
		fan.maxSinceSpin = pwm
	case !fan.spinning && pwm >= fan.breakaway:
		if fan.startAt.IsZero() {
			fan.startAt = now.Add(fan.spinUpDelay)
			fan.maxSinceSpin = 0
		}
	case !fan.spinning:
		fan.startAt = time.Time{}
	}
	if pwm > fan.maxSinceSpin {
		fan.maxSinceSpin = pwm
	}
	fan.physicalRpm(now)
	return nil
}

func (fan *kickTestFan) GetPwm() (int, error) {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	return fan.PWM, nil
}

func (fan *kickTestFan) setSpinning(spinning bool) {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	fan.spinning = spinning
	fan.startAt = time.Time{}
	fan.coastFrom = time.Time{}
}

func (fan *kickTestFan) isSpinning() bool {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	fan.physicalRpm(fan.now())
	return fan.spinning
}

func (fan *kickTestFan) setReadError(failing bool) {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	fan.readError = failing
}

// setStaleTach makes the tach report rpm until its next update (one tachInterval from now).
func (fan *kickTestFan) setStaleTach(rpm int) {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	fan.cachedRpm = rpm
	fan.cachedAt = fan.now()
	fan.cacheValid = true
}

func (fan *kickTestFan) pwmWrites() []int {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	return append([]int{}, fan.writes...)
}

func (fan *kickTestFan) GetRpm() (int, error) {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	if fan.readError {
		return 0, fmt.Errorf("tach read failed")
	}
	if len(fan.glitches) > 0 {
		glitch := fan.glitches[0]
		fan.glitches = fan.glitches[1:]
		return glitch, nil
	}
	now := fan.now()
	rpm := fan.physicalRpm(now)
	if fan.tachInterval <= 0 {
		return rpm, nil
	}
	if !fan.cacheValid || now.Sub(fan.cachedAt) >= fan.tachInterval {
		fan.cachedRpm = rpm
		fan.cachedAt = now
		fan.cacheValid = true
	}
	return fan.cachedRpm, nil
}

func (fan *kickTestFan) GetRpmAvg() float64 {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	return fan.rpmAvg
}

func (fan *kickTestFan) SetRpmAvg(rpm float64) {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	fan.rpmAvg = rpm
}

func (fan *kickTestFan) GetStartPwm() int {
	return fan.startPwm
}

func (fan *kickTestFan) GetConfig() configuration.FanConfig {
	config := fan.MockFan.GetConfig()
	config.HwMon = &configuration.HwMonFanConfig{}
	// derived from the analysis, not configured
	config.StartPwm = nil
	config.MinPwm = nil
	config.MaxPwm = nil
	config.KickStart = fan.kickStart
	config.SanityCheck.FanStalled.Enabled = configuration.DefaultTrueBool{}
	return config
}

func (fan *kickTestFan) Supports(feature fans.FeatureFlag) bool {
	if feature == fans.FeatureRpmSensor {
		return !fan.noRpmSensor
	}
	return true
}

// kickTest drives the controller of a kickTestFan like Run does: the update loop every 200ms, the RPM monitor
// every second (stallTestRpmPollingRate).
type kickTest struct {
	fan           *kickTestFan
	controller    *DefaultFanController
	clock         *util.FakeClock
	curveValue    *float64
	notifications []string
}

const kickTestUpdateRate = 200 * time.Millisecond

func newKickTest(t *testing.T, fan *kickTestFan, controlLoop control_loop.ControlLoop) *kickTest {
	t.Helper()
	setupStallTestConfig(t)
	curveValue := 0.0
	if controlLoop == nil {
		controlLoop = control_loop.NewDirectControlLoop(nil)
	}
	clock := util.NewFakeClock(time.Unix(1000, 0))
	fan.clock = clock
	controller := NewFanController(mockPersistence{hasSavedPwmData: true}, fan, MockCurve{ID: "curve", Value: &curveValue},
		controlLoop, kickTestUpdateRate, false)
	controller.pwmMapping = createOneToOnePwmMap()
	controller.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
	controller.pwmMapComputed = true
	controller.SetClock(clock)
	test := &kickTest{fan: fan, controller: controller, clock: clock, curveValue: &curveValue}
	controller.errorNotifier = func(title, format string, a ...any) {
		test.notifications = append(test.notifications, title+": "+fmt.Sprintf(format, a...))
	}
	// the RPM monitor has read the fan twice (see TestKickStart_FirstUpdateWithoutRpmReading)
	if !fan.noRpmSensor {
		test.monitorReadsTwice()
	}
	return test
}

// monitorReadsTwice lets the RPM monitor read the fan twice, a second apart (the clock ends where it started).
func (k *kickTest) monitorReadsTwice() {
	k.clock.Advance(-stallTestRpmPollingRate)
	k.controller.measureRpm(k.fan)
	k.clock.Advance(stallTestRpmPollingRate)
	k.controller.measureRpm(k.fan)
}

func (fan *kickTestFan) addGlitch(rpm int) {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	fan.glitches = append(fan.glitches, rpm)
}

// run runs the update loop and the RPM monitor for the given number of seconds.
func (k *kickTest) run(t *testing.T, seconds int) {
	t.Helper()
	for s := 0; s < seconds; s++ {
		for i := 0; i < int(stallTestRpmPollingRate/kickTestUpdateRate); i++ {
			k.clock.Advance(kickTestUpdateRate)
			require.NoError(t, k.controller.UpdateFanSpeed())
		}
		k.controller.measureRpm(k.fan)
	}
}

// updates runs n updates without the RPM monitor.
func (k *kickTest) updates(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		k.clock.Advance(kickTestUpdateRate)
		require.NoError(t, k.controller.UpdateFanSpeed())
	}
}

func (k *kickTest) pwm() int {
	pwm, _ := k.fan.GetPwm()
	return pwm
}

// setSlowestSpeed sets curve value 1, whose target is minPwm
func (k *kickTest) setSlowestSpeed() {
	*k.curveValue = 1
}

func (k *kickTest) withBreakaway(pwm int) *kickTest {
	k.controller.breakawayPwm = &pwm
	return k
}

// A fan at standstill gets a kick at the measured breakaway PWM value plus the margin until two RPM readings show
// it spinning, then its target, watched for a while. No blips afterward: no write above the target, no second kick.
func TestKickStart_StandstillKickSpinHandover(t *testing.T) {
	// GIVEN a stopped fan that needs PWM 116 to start, but keeps spinning down to 32, with the default control algorithm
	fan := newKickTestFan(116, 32)
	direct := configuration.DefaultControlAlgorithmConfig().Direct
	test := newKickTest(t, fan, control_loop.NewDirectControlLoopWithOptions(control_loop.DirectControlLoopOptions{
		TimeConstant: direct.TimeConstant.Duration(),
		Deadband:     direct.Deadband.Value(),
	})).withBreakaway(116)
	test.setSlowestSpeed()

	// WHEN the controller sets the slowest speed
	test.run(t, 1)

	// THEN the fan is kicked at 116 + 8 and spins; the kick ends after the second spinning reading
	assert.True(t, fan.isSpinning())
	assert.Equal(t, 124, test.pwm())
	test.run(t, 1)
	assert.Equal(t, 124, test.pwm())
	test.run(t, 1)
	assert.Equal(t, 32, test.pwm())

	// WHEN it runs on
	test.run(t, 30)

	// THEN it stays at its target: one kick, then only the target
	assert.True(t, fan.isSpinning())
	assert.Equal(t, []int{124, 32}, fan.pwmWrites())
	stats := test.controller.GetStatistics()
	assert.Equal(t, 1, stats.KickStartCount)
	assert.Equal(t, 0, stats.FailedKickStartCount)
	assert.Equal(t, 124, stats.KickStartPwm)
	assert.Equal(t, 0, stats.UnexpectedPwmValueCount)
	assert.Equal(t, 0, stats.StallCount)
	assert.Equal(t, kickPhaseNone, test.controller.kick.phase)
	assert.Empty(t, test.notifications)
}

// A fan that spins already isn't kicked, also not at the first update of a run.
func TestKickStart_AlreadySpinning_NoKick(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.setSpinning(true)
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()

	// the start is watched for the coast-down allowance (5s), plus the response delay and 2 readings
	test.run(t, 15)

	assert.Equal(t, []int{32}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartCount)
	assert.Equal(t, kickPhaseNone, test.controller.kick.phase)
}

// A target that changes while the fan is supposed to spin (after the start was watched) is no start from standstill:
// no kick, even if the fan stopped (that is what the stall checks are for), so it can't oscillate.
func TestKickStart_TargetChangeAboveStop_NoKick(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.setSpinning(true)
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()
	test.run(t, 10)

	// WHEN the fan stops (e.g. a target below what it needs), and the target changes
	fan.setSpinning(false)
	*test.curveValue = 50
	test.run(t, 10)

	// THEN
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartCount)
}

// A target that stops the fan is never kicked, and a fan that stopped and gets a spinning target again is kicked.
func TestKickStart_StopAndStartAgain(t *testing.T) {
	fan := newKickTestFan(116, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)

	// target 0: no kick
	test.run(t, 5)
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartCount)
	assert.False(t, fan.isSpinning())

	// start
	test.setSlowestSpeed()
	test.run(t, 4)
	assert.True(t, fan.isSpinning())
	assert.Equal(t, 32, test.pwm())

	// stop, start again: kicked again
	*test.curveValue = 0
	test.run(t, 3)
	assert.False(t, fan.isSpinning())
	test.setSlowestSpeed()
	test.run(t, 4)
	assert.True(t, fan.isSpinning())
	assert.Equal(t, 2, test.controller.GetStatistics().KickStartCount)
	// (PWM 0 at the start is set already)
	assert.Equal(t, []int{124, 32, 0, 124, 32}, fan.pwmWrites())
}

// A target that stops the fan during a kick ends the kick, without counting it as failed.
func TestKickStart_TargetStopsFanDuringKick(t *testing.T) {
	fan := newKickTestFan(200, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()
	test.run(t, 2)
	require.Equal(t, kickPhaseKicking, test.controller.kick.phase)

	*test.curveValue = 0
	test.run(t, 1)

	assert.Equal(t, kickPhaseNone, test.controller.kick.phase)
	assert.Equal(t, 0, test.pwm())
	assert.Equal(t, 0, test.controller.GetStatistics().FailedKickStartCount)
}

// A kick that doesn't start the fan is raised by a step after the timeout, until the fan starts. The raised value is
// used for the next start from standstill in this run.
func TestKickStart_FailedKickEscalates(t *testing.T) {
	// GIVEN a fan whose measured breakaway PWM value (116) is too low now: it needs 200
	fan := newKickTestFan(200, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()

	// WHEN
	test.run(t, 30)

	// THEN 124 (5s), 156 (5s), 188 (5s), 220: started
	assert.True(t, fan.isSpinning())
	assert.Equal(t, 32, test.pwm())
	assert.Equal(t, []int{124, 156, 188, 220, 32}, fan.pwmWrites())
	stats := test.controller.GetStatistics()
	assert.Equal(t, 4, stats.KickStartCount)
	assert.Equal(t, 3, stats.FailedKickStartCount)
	assert.Equal(t, 220, stats.KickStartPwm)
	// the stall check doesn't report the fan while its start is supervised
	assert.Empty(t, test.notifications)

	// WHEN it stops and starts again: the raised value is used right away
	*test.curveValue = 0
	test.run(t, 3)
	test.setSlowestSpeed()
	test.run(t, 10)
	assert.Equal(t, []int{124, 156, 188, 220, 32, 0, 220, 32}, fan.pwmWrites())
	assert.Equal(t, 3, test.controller.GetStatistics().FailedKickStartCount)
}

// After kickStartStepDownAfter kicks in a row that started the fan at once, a raised kick value is lowered by half a
// step, but never back to (or within half a step of) a value that failed in this run.
func TestKickStart_StepDownNeverReturnsToAFailedLevel(t *testing.T) {
	// GIVEN a fan that needs 130: 124 fails, 156 starts it
	fan := newKickTestFan(130, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()
	test.run(t, 15)
	require.Equal(t, 156, test.controller.kick.level)
	require.Equal(t, 1, test.controller.GetStatistics().FailedKickStartCount)

	// WHEN the fan starts at once many times
	for i := 0; i < 4*kickStartStepDownAfter; i++ {
		*test.curveValue = 0
		test.run(t, 2)
		test.setSlowestSpeed()
		test.run(t, 15)
		require.True(t, fan.isSpinning())
		require.Equal(t, kickPhaseNone, test.controller.kick.phase)
	}

	// THEN it is lowered to 140 (124 + half a step), and stays there: no recurring failed starts
	assert.Equal(t, 140, test.controller.kick.level)
	assert.Equal(t, 1, test.controller.GetStatistics().FailedKickStartCount)
}

// If even a kick at full speed doesn't start the fan, fan2go gives up: the fan gets its target, the stall check
// reports it after its reaction period, and it isn't kicked again (also not after an error restart of the controller).
func TestKickStart_GivesUpAtFullSpeed(t *testing.T) {
	// GIVEN a fan that doesn't start at all
	fan := newKickTestFan(999, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()

	// WHEN kicks at 124, 156, 188, 220, 252 and 255 (5s each) fail
	test.run(t, 30)
	assert.Equal(t, []int{124, 156, 188, 220, 252, 255}, fan.pwmWrites())
	assert.Empty(t, test.notifications)
	test.run(t, 1)

	// THEN it gets its target, and the stall check reports it after its reaction period
	assert.Equal(t, 32, test.pwm())
	assert.True(t, test.controller.kick.disabled)
	assert.Equal(t, 6, test.controller.GetStatistics().FailedKickStartCount)
	test.run(t, minPwmIncreaseRpmPollingPeriods+1)
	assert.Len(t, test.notifications, 1)

	// WHEN it is stopped and started again: no kick
	*test.curveValue = 0
	test.run(t, 2)
	test.setSlowestSpeed()
	test.run(t, 10)
	assert.Equal(t, 6, test.controller.GetStatistics().KickStartCount)
	assert.Equal(t, 32, test.pwm())

	// WHEN the controller restarts after an error (same controller): still no kick
	test.controller.resetRuntimeState()
	test.run(t, 10)
	assert.True(t, test.controller.kick.disabled)
	assert.Equal(t, 6, test.controller.GetStatistics().KickStartCount)
}

// A kick never goes above a configured maxPwm.
func TestKickStart_ConfiguredMaxPwmCapsTheKick(t *testing.T) {
	fan := newKickTestFan(999, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.controller.fan = &configuredFan{kickTestFan: fan, maxPwm: intPtr(150)}
	test.setSlowestSpeed()

	test.run(t, 30)

	assert.Equal(t, []int{124, 150, 32}, fan.pwmWrites())
}

func intPtr(v int) *int {
	return &v
}

// configuredFan is a kickTestFan with configured minPwm, startPwm and maxPwm (nil: derived).
type configuredFan struct {
	*kickTestFan
	minPwm, startPwm, maxPwm *int
}

func (fan *configuredFan) GetConfig() configuration.FanConfig {
	config := fan.kickTestFan.GetConfig()
	config.MinPwm = fan.minPwm
	config.StartPwm = fan.startPwm
	config.MaxPwm = fan.maxPwm
	return config
}

// Without a measured breakaway PWM value (e.g. data from an older version), the first kick is startPwm plus a
// quarter of the range up to full speed.
func TestKickStart_NoMeasuredBreakaway_UsesTheFallback(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.startPwm = 45
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()

	test.run(t, 15)

	// 45 + (255-45)/4 = 97: fails, 129 starts it
	assert.Equal(t, []int{97, 129, 32}, fan.pwmWrites())
}

// With mode auto, a configured startPwm is the first kick.
func TestKickStart_ConfiguredStartPwmIsTheKick(t *testing.T) {
	fan := newKickTestFan(116, 32)
	test := newKickTest(t, fan, nil).withBreakaway(60)
	test.controller.fan = &configuredFan{kickTestFan: fan, startPwm: intPtr(120)}
	test.setSlowestSpeed()

	test.run(t, 5)

	assert.Equal(t, []int{120, 32}, fan.pwmWrites())
}

// A configured startPwm without minPwm makes minPwm = startPwm: the kick value isn't above the target. The start is
// still watched: if the target doesn't start the fan, it is kicked one step above the target.
func TestKickStart_KickLevelNotAboveTarget_StillStartsTheFan(t *testing.T) {
	// GIVEN startPwm 120 (so minPwm 120), but the fan needs 140 from standstill
	fan := newKickTestFan(140, 32)
	fan.MinPWM = 120
	test := newKickTest(t, fan, nil)
	test.controller.fan = &configuredFan{kickTestFan: fan, startPwm: intPtr(120)}
	test.setSlowestSpeed()

	// WHEN
	test.run(t, 15)

	// THEN 120 is watched, the fan stands still: kicked at 152, then its target
	assert.True(t, fan.isSpinning())
	assert.Equal(t, []int{120, 152, 120}, fan.pwmWrites())
	assert.Empty(t, test.notifications)
}

// A target at or above the kick value that starts the fan: no kick.
func TestKickStart_TargetAboveKick_NoKick(t *testing.T) {
	fan := newKickTestFan(116, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	*test.curveValue = 255

	test.run(t, 10)

	assert.Equal(t, []int{255}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartCount)
}

// kickStart.mode: off sets the target right away, as before.
func TestKickStart_ModeOff(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.kickStart = &configuration.KickStartConfig{Mode: configuration.KickStartOff}
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()

	test.run(t, 10)

	assert.Equal(t, []int{32}, fan.pwmWrites())
	assert.False(t, fan.isSpinning())
}

// The RPM reading at the start shows a stale (cached) or coasting value although the fan stands still: the start is
// watched, and the fan is kicked once fresh readings show the standstill.
func TestKickStart_StaleHighReadingAtTheStart_StillKicks(t *testing.T) {
	// GIVEN a stopped fan whose driver caches the RPM for 1.5s and still reports 300 RPM
	fan := newKickTestFan(116, 32)
	fan.tachInterval = 1500 * time.Millisecond
	test := newKickTest(t, fan, nil).withBreakaway(116)
	fan.setStaleTach(300)
	test.setSlowestSpeed()

	// WHEN
	test.run(t, 30)

	// THEN
	assert.True(t, fan.isSpinning())
	assert.Equal(t, []int{32, 124, 32}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.GetStatistics().StallCount)
}

// A fan that is still coasting when it should start again is caught by its target: no kick.
func TestKickStart_CoastingFan_NoKick(t *testing.T) {
	// GIVEN a spinning fan that coasts for 6s after it stopped
	fan := newKickTestFan(116, 32)
	fan.coastDown = 6 * time.Second
	fan.setSpinning(true)
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()
	test.run(t, 10)

	// WHEN it is stopped and started again 2s later
	*test.curveValue = 0
	test.run(t, 2)
	test.setSlowestSpeed()
	test.run(t, 15)

	// THEN
	assert.True(t, fan.isSpinning())
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartCount)
}

// A tach that reports a new value only every 2s and a fan that needs 2s to spin up: with the measured timing, the
// kick isn't counted as failed before the readings can show the fan spinning.
func TestKickStart_SlowTachAndSpinUp_NoFalseFailure(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.tachInterval = 2 * time.Second
	fan.spinUpDelay = 2 * time.Second
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.controller.tachInterval = 2 * time.Second
	test.controller.spinUpDelay = 2 * time.Second
	test.setSlowestSpeed()

	test.run(t, 20)

	assert.True(t, fan.isSpinning())
	assert.Equal(t, []int{124, 32}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.GetStatistics().FailedKickStartCount)
	// response delay 0 + 2s + 2s, plus 2 tach intervals
	assert.Equal(t, 8*time.Second, test.controller.kickStartTimeout(configuration.KickStartConfig{}))
}

// A short kickStart.duration doesn't make a kick fail before the readings can show the fan spinning.
func TestKickStart_ShortDuration_NoFalseFailure(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.spinUpDelay = 2500 * time.Millisecond
	fan.kickStart = &configuration.KickStartConfig{Duration: configuration.SecondsOrDuration(time.Second)}
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()

	test.run(t, 15)

	assert.Equal(t, []int{124, 32}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.GetStatistics().FailedKickStartCount)
	assert.Equal(t, minKickStartTimeout+time.Second, test.controller.kickStartTimeout(fan.GetConfig().GetKickStart()))
}

// While reading the RPM fails, a kick isn't judged: no escalation. It isn't held forever either: after 3 kick
// timeouts, the fan gets its target, like a fan without an RPM sensor.
func TestKickStart_RpmReadErrorsDuringKick_NoEscalation(t *testing.T) {
	fan := newKickTestFan(200, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()
	test.run(t, 1)
	require.Equal(t, 124, test.pwm())

	// WHEN reading the RPM fails for a while, shorter than 3 kick timeouts (15s)
	fan.setReadError(true)
	test.run(t, 10)

	// THEN the kick goes on, nothing failed
	assert.Equal(t, 124, test.pwm())
	assert.Equal(t, 0, test.controller.GetStatistics().FailedKickStartCount)

	// WHEN reading works again: judged then
	fan.setReadError(false)
	test.run(t, 20)
	assert.True(t, fan.isSpinning())
	assert.Equal(t, []int{124, 156, 188, 220, 32}, fan.pwmWrites())
}

// A kick while reading the RPM keeps failing ends unjudged after 3 kick timeouts: the fan gets its target, nothing
// counts as failed, nothing is escalated.
func TestKickStart_RpmReadErrorsForLong_KickEndsUnjudged(t *testing.T) {
	fan := newKickTestFan(200, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()
	test.run(t, 1)
	require.Equal(t, 124, test.pwm())

	fan.setReadError(true)
	test.run(t, 600)

	assert.Equal(t, 32, test.pwm())
	assert.Equal(t, []int{124, 32}, fan.pwmWrites())
	assert.Equal(t, kickPhaseNone, test.controller.kick.phase)
	assert.Equal(t, 0, test.controller.GetStatistics().FailedKickStartCount)
	assert.False(t, test.controller.kick.disabled)
}

// A fan that broke free with a kick, but stalls again at its target (it wasn't up to speed): the watch after the
// kick sees it standing still and kicks it once more, one step higher. That kick keeps it spinning, so it is used
// for later starts. Neither counts as a failed kick.
func TestKickStart_StallAfterHandover_KickedAgain(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.needsKickAtLeast = 150
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()

	test.run(t, 30)

	assert.True(t, fan.isSpinning())
	assert.Equal(t, []int{124, 32, 156, 32}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.GetStatistics().FailedKickStartCount)
	assert.Equal(t, 156, test.controller.kick.level)
	assert.Empty(t, test.notifications)
}

// A target below the PWM value that keeps the fan spinning (e.g. a configured minPwm that is too low): the kick
// makes the fan spin, it stops at its target, gets one more kick, stops again, and the start ends with a warning.
// No failed kicks, no escalation to full speed, the kick start isn't disabled.
func TestKickStart_TargetBelowKeepSpinning_NoEscalation(t *testing.T) {
	// GIVEN a fan that needs 116 to start and 32 to keep spinning, that coasts for 2s, with minPwm 20
	fan := newKickTestFan(116, 32)
	fan.MinPWM = 20
	fan.coastDown = 2 * time.Second
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()

	// WHEN
	test.run(t, 120)

	// THEN
	assert.Equal(t, []int{124, 20, 156, 20}, fan.pwmWrites())
	stats := test.controller.GetStatistics()
	assert.Equal(t, 2, stats.KickStartCount)
	assert.Equal(t, 0, stats.FailedKickStartCount)
	assert.False(t, test.controller.kick.disabled)
	assert.Equal(t, 124, test.controller.kick.level)
	assert.Equal(t, kickPhaseNone, test.controller.kick.phase)

	// WHEN it is stopped and started again: at most 2 kicks again, the warning isn't repeated
	*test.curveValue = 0
	test.run(t, 5)
	test.setSlowestSpeed()
	test.run(t, 60)
	assert.Equal(t, []int{124, 20, 156, 20, 0, 124, 20, 156, 20}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.GetStatistics().FailedKickStartCount)
	assert.False(t, test.controller.kick.disabled)
}

// A fan that coasts longer than the response delay after its target turned out too low: the watch lasts the
// coast-down allowance and needs spinning readings after it, so the stop is seen.
func TestKickStart_LongCoastDownAfterHandover_StopIsSeen(t *testing.T) {
	// GIVEN a fan that coasts for 8s (measured by the analysis), with a target that doesn't keep it spinning
	fan := newKickTestFan(116, 32)
	fan.MinPWM = 20
	fan.coastDown = 8 * time.Second
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.controller.coastDown = 8 * time.Second
	test.setSlowestSpeed()

	// WHEN
	test.run(t, 120)

	// THEN the stop is seen and handled, not counted as a successful kick
	assert.Equal(t, []int{124, 20, 156, 20}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.kick.successStreak)
}

// The default coast-down allowance (5s) covers a fan that coasts for 4s without measured data.
func TestKickStart_CoastDownWithinDefaultAllowance(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.MinPWM = 20
	fan.coastDown = 4 * time.Second
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()

	test.run(t, 60)

	assert.Equal(t, []int{124, 20, 156, 20}, fan.pwmWrites())
}

// A fan without an RPM sensor isn't kicked with mode auto without a configured value (logged once).
func TestKickStart_NoRpmSensor_ModeAuto_NoKick(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.noRpmSensor = true
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()

	log := captureLog(t, func() {
		test.updates(t, 50)
		*test.curveValue = 0
		test.updates(t, 5)
		test.setSlowestSpeed()
		test.updates(t, 5)
	})

	assert.Equal(t, []int{32, 0, 32}, fan.pwmWrites())
	assert.Equal(t, 1, countOccurrences(log, "kickStart.mode: auto can't kick this fan"))
}

func countOccurrences(s string, sub string) int {
	count := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			count++
		}
	}
	return count
}

// Mode auto with an explicit value kicks a fan without an RPM sensor like mode fixed: kickStart.pwm, or a configured
// startPwm above minPwm.
func TestKickStart_NoRpmSensor_ModeAutoWithExplicitValue_TimedKick(t *testing.T) {
	tests := []struct {
		name   string
		modify func(fan *kickTestFan) fans.Fan
		kick   int
	}{
		{"kickStart.pwm", func(fan *kickTestFan) fans.Fan {
			fan.kickStart = &configuration.KickStartConfig{Pwm: intPtr(150)}
			return fan
		}, 150},
		{"startPwm above minPwm", func(fan *kickTestFan) fans.Fan {
			return &configuredFan{kickTestFan: fan, minPwm: intPtr(32), startPwm: intPtr(120)}
		}, 120},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fan := newKickTestFan(116, 32)
			fan.noRpmSensor = true
			test := newKickTest(t, fan, nil)
			test.controller.fan = tt.modify(fan)
			test.setSlowestSpeed()

			// held for the default 3s
			test.updates(t, 14)
			assert.Equal(t, tt.kick, test.pwm())
			test.updates(t, 2)
			assert.Equal(t, 32, test.pwm())
			test.updates(t, 20)
			assert.Equal(t, []int{tt.kick, 32}, fan.pwmWrites())
		})
	}
}

// A fan without an RPM sensor with mode fixed is kicked at kickStart.pwm for kickStart.duration when it should start,
// and only then.
func TestKickStart_NoRpmSensor_ModeFixed_TimedKick(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.noRpmSensor = true
	fan.kickStart = &configuration.KickStartConfig{Mode: configuration.KickStartFixed, Pwm: intPtr(150),
		Duration: configuration.SecondsOrDuration(2 * time.Second)}
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()

	// the first update of a run: standstill is assumed
	test.updates(t, 1)
	assert.Equal(t, 150, test.pwm())
	test.updates(t, 9)
	assert.Equal(t, 150, test.pwm())
	test.updates(t, 1)
	assert.Equal(t, 32, test.pwm())
	test.updates(t, 20)
	assert.Equal(t, []int{150, 32}, fan.pwmWrites())

	// stop and start again
	*test.curveValue = 0
	test.updates(t, 5)
	test.setSlowestSpeed()
	test.updates(t, 15)
	assert.Equal(t, []int{150, 32, 0, 150, 32}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.GetStatistics().FailedKickStartCount)
}

// Mode fixed with an RPM sensor ends the kick once the fan spins, and doesn't raise kickStart.pwm.
func TestKickStart_ModeFixed_WithRpmSensor(t *testing.T) {
	fan := newKickTestFan(200, 32)
	fan.kickStart = &configuration.KickStartConfig{Mode: configuration.KickStartFixed, Pwm: intPtr(150)}
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()

	test.run(t, 20)

	// 150 doesn't start the fan: no escalation, the fan gets its target after 5s
	assert.Equal(t, []int{150, 32}, fan.pwmWrites())
	assert.Equal(t, 1, test.controller.GetStatistics().FailedKickStartCount)
}

// A fan with neverStop is kicked first; the minPwm offset isn't raised when the kick starts it.
func TestKickStart_NeverStop_KickInsteadOfMinPwmRaise(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.shouldNeverStop = true
	test := newKickTest(t, fan, nil).withBreakaway(116)

	test.run(t, 30)

	assert.True(t, fan.isSpinning())
	assert.Equal(t, 32, test.pwm())
	assert.Equal(t, 0, test.controller.GetStatistics().IncreasedMinPwmCount)
	assert.Equal(t, []int{124, 32}, fan.pwmWrites())
}

// If the kicks fail, a fan with neverStop falls back to raising its minPwm offset, after its reaction period.
func TestKickStart_NeverStop_FallsBackToMinPwmRaiseWhenKicksFail(t *testing.T) {
	fan := newKickTestFan(999, 32)
	fan.shouldNeverStop = true
	test := newKickTest(t, fan, nil).withBreakaway(116)

	// six failed kicks of 5s each, no raise meanwhile
	test.run(t, 30)
	assert.Equal(t, 0, test.controller.GetStatistics().IncreasedMinPwmCount)
	test.run(t, 1)
	assert.Equal(t, 32, test.pwm())

	// THEN the minPwm offset is raised once the fan had time to react to its target
	test.run(t, minPwmIncreaseRpmPollingPeriods+1)
	assert.Equal(t, 1, test.controller.GetStatistics().IncreasedMinPwmCount)
}

// A file or cmd fan has no measured RPM curve: mode auto only supervises it once its tach was seen working in this run.
func TestKickStart_FileFan_NeedsTachSeenWorking(t *testing.T) {
	fan := newKickTestFan(116, 32)
	test := newKickTest(t, fan, nil)
	test.controller.fan = &fileLikeFan{kickTestFan: fan}
	test.setSlowestSpeed()

	test.updates(t, 25)
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartCount)

	// once seen spinning, a later start is kicked
	fan.setSpinning(true)
	test.controller.measureRpm(fan)
	*test.curveValue = 0
	test.run(t, 2)
	test.setSlowestSpeed()
	test.run(t, 2)
	assert.Equal(t, 1, test.controller.GetStatistics().KickStartCount)
}

type fileLikeFan struct {
	*kickTestFan
}

func (fan *fileLikeFan) GetConfig() configuration.FanConfig {
	config := fan.kickTestFan.GetConfig()
	config.HwMon = nil
	config.File = &configuration.FileFanConfig{}
	return config
}

// A curve failure ends a kick; onSensorFailure decides the PWM value.
func TestKickStart_CurveFailureAbortsTheKick(t *testing.T) {
	fan := newKickTestFan(200, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()
	test.run(t, 1)
	require.Equal(t, kickPhaseKicking, test.controller.kick.phase)

	failing := test.controller.getCurve().(MockCurve)
	failing.Err = fmt.Errorf("sensor stale")
	test.controller.UpdateCurve(failing)
	test.clock.Advance(kickTestUpdateRate)
	require.ErrorIs(t, test.controller.UpdateFanSpeed(), ErrCurveEvaluation)

	assert.Equal(t, kickPhaseNone, test.controller.kick.phase)
	assert.Equal(t, 255, test.pwm())
}

// A failed write neither counts a kick nor starts its timer.
func TestKickStart_FailedWrite_NotCounted(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.setPwmAlwaysFails = true
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()

	test.updates(t, 3)

	assert.Equal(t, 0, test.controller.GetStatistics().KickStartCount)
	assert.Equal(t, kickPhaseNone, test.controller.kick.phase)
}

// A new run starts over (except a disabled kick start, see TestKickStart_GivesUpAtFullSpeed).
func TestKickStart_ResetRuntimeState(t *testing.T) {
	fan := newKickTestFan(200, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()
	test.run(t, 20)
	require.Equal(t, 220, test.controller.kick.level)

	test.controller.resetRuntimeState()

	assert.Equal(t, kickStartState{}, test.controller.kick)
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartPwm)
}

// A kick to full speed on an it87 chip whose pwm_enable reads 0 at PWM 255 (known hardware quirk): fan2go doesn't
// mistake it for a third party that changed the control mode.
func TestKickStart_It87FullDutyKick(t *testing.T) {
	setupStallTestConfig(t)
	fan, pwmEnablePath := newFullDutyHwMonFan(t, "it8665", true)
	config := fan.GetConfig()
	require.NoError(t, os.WriteFile(config.HwMon.RpmInputPath, []byte("0"), 0o644))
	require.NoError(t, os.WriteFile(config.HwMon.PwmPath, []byte("0"), 0o644))
	require.NoError(t, os.WriteFile(pwmEnablePath, []byte("1"), 0o644))
	require.NoError(t, fan.AttachFanRpmCurveData(&map[int]float64{0: 0, 40: 300, 255: 1500}))
	curveValue := 1.0
	controller := NewFanController(mockPersistence{hasSavedPwmData: true}, fan, MockCurve{ID: "curve", Value: &curveValue},
		control_loop.NewDirectControlLoop(nil), kickTestUpdateRate, false)
	controller.pwmMapping = createOneToOnePwmMap()
	controller.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
	controller.pwmMapComputed = true
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller.SetClock(clock)
	breakaway := 250
	controller.breakawayPwm = &breakaway
	// the RPM monitor has seen the standstill
	controller.measureRpm(fan)
	clock.Advance(stallTestRpmPollingRate)
	controller.measureRpm(fan)

	// WHEN the fan is kicked at full speed, and the chip reports pwm_enable 0 for it
	clock.Advance(kickTestUpdateRate)
	require.NoError(t, controller.UpdateFanSpeed())
	pwm, err := util.ReadIntFromFile(config.HwMon.PwmPath)
	require.NoError(t, err)
	require.Equal(t, 255, pwm)
	require.NoError(t, os.WriteFile(pwmEnablePath, []byte("0"), 0o644))
	for i := 0; i < 60; i++ {
		clock.Advance(kickTestUpdateRate)
		require.NoError(t, controller.UpdateFanSpeed())
	}

	// THEN the mode is left alone, and the readback matches
	assert.Equal(t, "0", readPwmEnableFile(t, pwmEnablePath))
	assert.Equal(t, 0, controller.GetStatistics().UnexpectedPwmValueCount)
	assert.Equal(t, filepath.Base(config.HwMon.PwmPath), "pwm2")
}

// At the first update of a run, the RPM monitor may not have read the fan yet: a single reading doesn't decide, the
// start is watched, and the fan is kicked once the readings show the standstill.
func TestKickStart_FirstUpdateWithoutRpmReading(t *testing.T) {
	fan := newKickTestFan(116, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.controller.resetRuntimeState()
	test.setSlowestSpeed()

	test.run(t, 15)

	assert.True(t, fan.isSpinning())
	assert.Equal(t, []int{32, 124, 32}, fan.pwmWrites())
}

// A one-off wrong reading (e.g. another program accessed the chip at the same time) of a fan that spins doesn't make
// fan2go kick it.
func TestKickStart_OneOffLowGlitchOnSpinningFan_NoKick(t *testing.T) {
	// GIVEN a spinning, coasting fan that is stopped and started again within a second
	fan := newKickTestFan(116, 32)
	fan.coastDown = 10 * time.Second
	fan.setSpinning(true)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()
	test.run(t, 10)
	*test.curveValue = 0
	test.updates(t, 1)

	// WHEN the reading at the start, and later one of the monitor, is a glitch
	fan.addGlitch(20)
	test.setSlowestSpeed()
	test.updates(t, 1)
	fan.addGlitch(57)
	test.run(t, 2)
	fan.addGlitch(0)
	test.run(t, 15)

	// THEN
	assert.True(t, fan.isSpinning())
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartCount)
}

// A one-off reading that shows a kicked fan spinning, although it doesn't, doesn't end the kick.
func TestKickStart_OneOffSpinningGlitchDuringKick_NoHandover(t *testing.T) {
	fan := newKickTestFan(200, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()
	test.run(t, 1)
	require.Equal(t, 124, test.pwm())

	fan.addGlitch(800)
	test.run(t, 3)

	assert.Equal(t, 124, test.pwm())
	assert.Equal(t, []int{124}, fan.pwmWrites())
}

// A wrong reading that the driver caches, so the RPM monitor reads it twice, counts once: no handover.
func TestKickStart_GlitchCachedByTheDriver_NoHandover(t *testing.T) {
	// GIVEN a driver that updates the RPM value every 2.5s (measured), and a kick that doesn't start the fan
	fan := newKickTestFan(200, 32)
	fan.tachInterval = 2500 * time.Millisecond
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.controller.tachInterval = 2500 * time.Millisecond
	test.setSlowestSpeed()
	test.run(t, 2)
	require.Equal(t, 124, test.pwm())

	// WHEN the driver reads a wrong value and reports it until its next update
	fan.setStaleTach(830)
	test.run(t, 2)
	test.updates(t, 1)

	// THEN
	assert.Equal(t, 124, test.pwm())
	assert.Equal(t, []int{124}, fan.pwmWrites())
}

// The update loop runs less often than the RPM monitor: the samples between two updates count, a kick works as usual.
func TestKickStart_UpdateSlowerThanRpmPolling(t *testing.T) {
	fan := newKickTestFan(116, 32)
	test := newKickTest(t, fan, nil).withBreakaway(116)
	test.setSlowestSpeed()

	// WHEN the update loop runs every 3s, the RPM monitor every second
	for i := 0; i < 10; i++ {
		test.clock.Advance(stallTestRpmPollingRate)
		test.controller.measureRpm(fan)
		test.clock.Advance(stallTestRpmPollingRate)
		test.controller.measureRpm(fan)
		test.clock.Advance(stallTestRpmPollingRate)
		test.controller.measureRpm(fan)
		require.NoError(t, test.controller.UpdateFanSpeed())
	}

	// THEN
	assert.True(t, fan.isSpinning())
	assert.Equal(t, []int{124, 32}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.GetStatistics().FailedKickStartCount)
}

// The kick start judges a sample as a whole (value, time, streak), never a value of one reading with the time or
// count of another.
func TestKickStart_RpmSample_IsConsistent(t *testing.T) {
	fan := newKickTestFan(116, 32)
	test := newKickTest(t, fan, nil)
	c := test.controller
	start := test.clock.Now()

	c.rpmSample.Store(nil)
	c.recordRpmSample(0, start)
	c.recordRpmSample(830, start.Add(time.Second))
	// a value of another reading, stored on its own, doesn't change the sample
	c.lastRpmReading.Store(830)
	spinning, standstill := c.kickStartReadings(time.Time{})
	assert.False(t, spinning)
	assert.False(t, standstill)

	c.recordRpmSample(840, start.Add(2*time.Second))
	spinning, standstill = c.kickStartReadings(start.Add(time.Second))
	assert.True(t, spinning)
	assert.False(t, standstill)
	// both readings must be taken after judgeFrom
	spinning, _ = c.kickStartReadings(start.Add(1500 * time.Millisecond))
	assert.False(t, spinning)
	sample := c.rpmSample.Load()
	assert.Equal(t, uint64(3), sample.seq)
	assert.Equal(t, 2, sample.streak)

	// the same value within 3/4 of a tach interval (default 1s) is the same sample
	c.recordRpmSample(840, start.Add(2500*time.Millisecond))
	assert.Same(t, sample, c.rpmSample.Load())
	// a later one is a new sample
	c.recordRpmSample(840, start.Add(3*time.Second))
	assert.Equal(t, 3, c.rpmSample.Load().streak)
}

// The RPM monitor and the update loop run concurrently: the race detector checks the sample handover.
func TestKickStart_RpmSample_ConcurrentMonitor(t *testing.T) {
	fan := newKickTestFan(116, 32)
	test := newKickTest(t, fan, nil)
	c := test.controller
	start := test.clock.Now()
	c.rpmSample.Store(nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= 1000; i++ {
			c.recordRpmSample(i%2*800, start.Add(time.Duration(i)*time.Second))
		}
	}()
	for i := 0; i < 1000; i++ {
		spinning, standstill := c.kickStartReadings(time.Time{})
		// alternating values never make 2 in a row
		require.False(t, spinning || standstill)
	}
	<-done
}
