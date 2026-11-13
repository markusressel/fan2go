package controller

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kickTestFan is an analyzed hwmon-like fan with static friction: standing still, it only starts at breakaway
// (raw PWM) or more; spinning, it keeps spinning down to keepSpinning. It spins up and stops right away.
type kickTestFan struct {
	MockFan
	breakaway    int
	keepSpinning int
	startPwm     int
	noRpmSensor  bool
	kickStart    *configuration.KickStartConfig

	fanMu    sync.Mutex
	spinning bool
	rpmAvg   float64
	writes   []int
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

func (fan *kickTestFan) SetPwm(pwm int) error {
	if err := fan.MockFan.SetPwm(pwm); err != nil {
		return err
	}
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	fan.writes = append(fan.writes, pwm)
	if fan.spinning && pwm < fan.keepSpinning {
		fan.spinning = false
	} else if !fan.spinning && pwm >= fan.breakaway {
		fan.spinning = true
	}
	return nil
}

func (fan *kickTestFan) setSpinning(spinning bool) {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	fan.spinning = spinning
}

func (fan *kickTestFan) isSpinning() bool {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	return fan.spinning
}

func (fan *kickTestFan) pwmWrites() []int {
	fan.fanMu.Lock()
	defer fan.fanMu.Unlock()
	return append([]int{}, fan.writes...)
}

func (fan *kickTestFan) GetRpm() (int, error) {
	pwm, _ := fan.GetPwm()
	if !fan.isSpinning() {
		return 0, nil
	}
	return 100 + 3*pwm, nil
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
	controller := NewFanController(mockPersistence{hasSavedPwmData: true}, fan, MockCurve{ID: "curve", Value: &curveValue},
		controlLoop, kickTestUpdateRate, false)
	controller.pwmMapping = createOneToOnePwmMap()
	controller.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
	controller.pwmMapComputed = true
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller.SetClock(clock)
	test := &kickTest{fan: fan, controller: controller, clock: clock, curveValue: &curveValue}
	controller.errorNotifier = func(title, format string, a ...any) {
		test.notifications = append(test.notifications, title+": "+fmt.Sprintf(format, a...))
	}
	return test
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

func (k *kickTest) pwm() int {
	pwm, _ := k.fan.GetPwm()
	return pwm
}

// targetForCurveValue1 is the target of curve value 1 (the slowest speed): minPwm
func (k *kickTest) setSlowestSpeed() {
	*k.curveValue = 1
}

// A fan at standstill gets a kick at the measured breakaway PWM value plus the margin, until the RPM monitor sees
// it spinning, then its target. No blips afterward: no write above the target, no second kick.
func TestKickStart_StandstillKickSpinHandover(t *testing.T) {
	// GIVEN a stopped fan that needs PWM 116 to start, but keeps spinning down to 32, with the default control algorithm
	fan := newKickTestFan(116, 32)
	direct := configuration.DefaultControlAlgorithmConfig().Direct
	test := newKickTest(t, fan, control_loop.NewDirectControlLoopWithOptions(control_loop.DirectControlLoopOptions{
		TimeConstant: direct.TimeConstant.Duration(),
		Deadband:     direct.Deadband.Value(),
	}))
	breakaway := 116
	test.controller.breakawayPwm = &breakaway
	test.setSlowestSpeed()

	// WHEN the controller sets the slowest speed
	test.run(t, 1)

	// THEN the fan is kicked at 116 + 8 and spins, the kick ends with the next update after the RPM reading
	assert.True(t, fan.isSpinning())
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
	assert.Empty(t, test.notifications)
}

// A fan that spins already isn't kicked, also not at the first update of a Run.
func TestKickStart_AlreadySpinning_NoKick(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.setSpinning(true)
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()

	test.run(t, 10)

	assert.Equal(t, []int{32}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartCount)
}

// A target that should make the fan spin, while the last one did too, is no start from standstill: no kick, even
// if the fan stopped (that is what the stall checks are for).
func TestKickStart_TargetChangeAboveStop_NoKick(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.setSpinning(true)
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()
	test.run(t, 2)

	// WHEN the fan stops (e.g. a target below what it needs), and the target changes
	fan.setSpinning(false)
	*test.curveValue = 50
	test.run(t, 2)

	// THEN
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartCount)
}

// A target that stops the fan is never kicked, and a fan that stopped and gets a spinning target again is kicked
// once it stands still.
func TestKickStart_StopAndStartAgain(t *testing.T) {
	fan := newKickTestFan(116, 32)
	test := newKickTest(t, fan, nil)
	breakaway := 116
	test.controller.breakawayPwm = &breakaway

	// target 0: no kick
	test.run(t, 5)
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartCount)
	assert.False(t, fan.isSpinning())

	// start
	test.setSlowestSpeed()
	test.run(t, 3)
	assert.True(t, fan.isSpinning())
	assert.Equal(t, 32, test.pwm())

	// stop, start again: kicked again
	*test.curveValue = 0
	test.run(t, 3)
	assert.False(t, fan.isSpinning())
	test.setSlowestSpeed()
	test.run(t, 3)
	assert.True(t, fan.isSpinning())
	assert.Equal(t, 2, test.controller.GetStatistics().KickStartCount)
	// (PWM 0 at the start is set already)
	assert.Equal(t, []int{124, 32, 0, 124, 32}, fan.pwmWrites())
}

// A target that stops the fan during a kick ends the kick, without counting it as failed.
func TestKickStart_TargetStopsFanDuringKick(t *testing.T) {
	fan := newKickTestFan(200, 32)
	test := newKickTest(t, fan, nil)
	breakaway := 116
	test.controller.breakawayPwm = &breakaway
	test.setSlowestSpeed()
	test.run(t, 2)
	require.True(t, test.controller.kick.active)

	*test.curveValue = 0
	test.run(t, 1)

	assert.False(t, test.controller.kick.active)
	assert.Equal(t, 0, test.pwm())
	assert.Equal(t, 0, test.controller.GetStatistics().FailedKickStartCount)
}

// A kick that doesn't start the fan is raised by a step after kickStart.duration, until the fan starts. The raised
// value is used for the next start from standstill in this Run.
func TestKickStart_FailedKickEscalates(t *testing.T) {
	// GIVEN a fan whose measured breakaway PWM value (116) is too low now: it needs 200
	fan := newKickTestFan(200, 32)
	test := newKickTest(t, fan, nil)
	breakaway := 116
	test.controller.breakawayPwm = &breakaway
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
	// the stall check doesn't report the fan while it is kicked
	assert.Empty(t, test.notifications)

	// WHEN it stops and starts again: the raised value is used right away
	*test.curveValue = 0
	test.run(t, 3)
	test.setSlowestSpeed()
	test.run(t, 3)
	assert.Equal(t, []int{124, 156, 188, 220, 32, 0, 220, 32}, fan.pwmWrites())
	assert.Equal(t, 3, test.controller.GetStatistics().FailedKickStartCount)
}

// After kickStartStepDownAfter kicks in a row that started the fan at once, a raised kick value is lowered by half a
// step again, never below the initial value.
func TestKickStart_RaisedKickStepsDown(t *testing.T) {
	fan := newKickTestFan(130, 32)
	test := newKickTest(t, fan, nil)
	breakaway := 116
	test.controller.breakawayPwm = &breakaway
	test.setSlowestSpeed()
	test.run(t, 10)
	require.Equal(t, 156, test.controller.kick.level)

	// WHEN the fan starts at once kickStartStepDownAfter times
	for i := 0; i < kickStartStepDownAfter; i++ {
		*test.curveValue = 0
		test.run(t, 2)
		test.setSlowestSpeed()
		test.run(t, 2)
		require.True(t, fan.isSpinning())
	}

	// THEN it is lowered by half a step (16)
	assert.Equal(t, 140, test.controller.kick.level)
}

// If even a kick at full speed doesn't start the fan, fan2go gives up: the fan gets its target, the stall check
// reports it after its reaction period, and it isn't kicked again in this Run.
func TestKickStart_GivesUpAtFullSpeed(t *testing.T) {
	// GIVEN a fan that doesn't start at all
	fan := newKickTestFan(999, 32)
	test := newKickTest(t, fan, nil)
	breakaway := 116
	test.controller.breakawayPwm = &breakaway
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
}

// A kick never goes above a configured maxPwm.
func TestKickStart_ConfiguredMaxPwmCapsTheKick(t *testing.T) {
	fan := newKickTestFan(999, 32)
	fan.MaxPWM = 150
	test := newKickTest(t, fan, nil)
	test.controller.fan = &configuredMaxPwmFan{kickTestFan: fan, maxPwm: 150}
	breakaway := 116
	test.controller.breakawayPwm = &breakaway
	test.setSlowestSpeed()

	test.run(t, 30)

	assert.Equal(t, []int{124, 150, 32}, fan.pwmWrites())
}

type configuredMaxPwmFan struct {
	*kickTestFan
	maxPwm int
}

func (fan *configuredMaxPwmFan) GetConfig() configuration.FanConfig {
	config := fan.kickTestFan.GetConfig()
	config.MaxPwm = &fan.maxPwm
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
	test := newKickTest(t, fan, nil)
	test.controller.fan = &configuredStartPwmFan{kickTestFan: fan, startPwm: 120}
	breakaway := 60
	test.controller.breakawayPwm = &breakaway
	test.setSlowestSpeed()

	test.run(t, 5)

	assert.Equal(t, []int{120, 32}, fan.pwmWrites())
}

type configuredStartPwmFan struct {
	*kickTestFan
	startPwm int
}

func (fan *configuredStartPwmFan) GetConfig() configuration.FanConfig {
	config := fan.kickTestFan.GetConfig()
	config.StartPwm = &fan.startPwm
	return config
}

// A target at or above the kick value starts the fan by itself: no kick.
func TestKickStart_TargetAboveKick_NoKick(t *testing.T) {
	fan := newKickTestFan(116, 32)
	test := newKickTest(t, fan, nil)
	breakaway := 116
	test.controller.breakawayPwm = &breakaway
	*test.curveValue = 255

	test.run(t, 5)

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

// A fan without an RPM sensor isn't kicked with mode auto: fan2go can see neither standstill nor whether a kick
// started it.
func TestKickStart_NoRpmSensor_ModeAuto_NoKick(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.noRpmSensor = true
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()

	for i := 0; i < 50; i++ {
		test.clock.Advance(kickTestUpdateRate)
		require.NoError(t, test.controller.UpdateFanSpeed())
	}

	assert.Equal(t, []int{32}, fan.pwmWrites())
}

// A fan without an RPM sensor with mode fixed is kicked at kickStart.pwm for kickStart.duration when it should start,
// and only then.
func TestKickStart_NoRpmSensor_ModeFixed_TimedKick(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.noRpmSensor = true
	pwm := 150
	fan.kickStart = &configuration.KickStartConfig{Mode: configuration.KickStartFixed, Pwm: &pwm,
		Duration: configuration.SecondsOrDuration(2 * time.Second)}
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()
	update := func(n int) {
		for i := 0; i < n; i++ {
			test.clock.Advance(kickTestUpdateRate)
			require.NoError(t, test.controller.UpdateFanSpeed())
		}
	}

	// the first update of a Run: standstill is assumed
	update(1)
	assert.Equal(t, 150, test.pwm())
	update(9)
	assert.Equal(t, 150, test.pwm())
	update(1)
	assert.Equal(t, 32, test.pwm())
	update(20)
	assert.Equal(t, []int{150, 32}, fan.pwmWrites())

	// stop and start again
	*test.curveValue = 0
	update(5)
	test.setSlowestSpeed()
	update(15)
	assert.Equal(t, []int{150, 32, 0, 150, 32}, fan.pwmWrites())
	assert.Equal(t, 0, test.controller.GetStatistics().FailedKickStartCount)
}

// Mode fixed with an RPM sensor ends the kick once the fan spins, and doesn't raise kickStart.pwm.
func TestKickStart_ModeFixed_WithRpmSensor(t *testing.T) {
	fan := newKickTestFan(200, 32)
	pwm := 150
	fan.kickStart = &configuration.KickStartConfig{Mode: configuration.KickStartFixed, Pwm: &pwm}
	test := newKickTest(t, fan, nil)
	test.setSlowestSpeed()

	test.run(t, 10)

	// 150 doesn't start the fan: no escalation, the fan gets its target after 5s
	assert.Equal(t, []int{150, 32}, fan.pwmWrites())
	assert.Equal(t, 1, test.controller.GetStatistics().FailedKickStartCount)
}

// A fan with neverStop is kicked first; the minPwm offset isn't raised when the kick starts it.
func TestKickStart_NeverStop_KickInsteadOfMinPwmRaise(t *testing.T) {
	fan := newKickTestFan(116, 32)
	fan.shouldNeverStop = true
	test := newKickTest(t, fan, nil)
	breakaway := 116
	test.controller.breakawayPwm = &breakaway

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
	test := newKickTest(t, fan, nil)
	breakaway := 116
	test.controller.breakawayPwm = &breakaway

	// six failed kicks of 5s each, no raise meanwhile
	test.run(t, 30)
	assert.Equal(t, 0, test.controller.GetStatistics().IncreasedMinPwmCount)
	test.run(t, 1)
	assert.Equal(t, 32, test.pwm())

	// THEN the minPwm offset is raised once the fan had time to react to its target
	test.run(t, minPwmIncreaseRpmPollingPeriods+1)
	assert.Equal(t, 1, test.controller.GetStatistics().IncreasedMinPwmCount)
}

// A file or cmd fan has no measured RPM curve: mode auto only kicks it once its tach was seen working in this Run.
func TestKickStart_FileFan_NeedsTachSeenWorking(t *testing.T) {
	fan := newKickTestFan(116, 32)
	test := newKickTest(t, fan, nil)
	test.controller.fan = &fileLikeFan{kickTestFan: fan}
	test.setSlowestSpeed()

	test.run(t, 5)
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
	test := newKickTest(t, fan, nil)
	breakaway := 116
	test.controller.breakawayPwm = &breakaway
	test.setSlowestSpeed()
	test.run(t, 1)
	require.True(t, test.controller.kick.active)

	failing := test.controller.getCurve().(MockCurve)
	failing.Err = fmt.Errorf("sensor stale")
	test.controller.UpdateCurve(failing)
	test.clock.Advance(kickTestUpdateRate)
	require.ErrorIs(t, test.controller.UpdateFanSpeed(), ErrCurveEvaluation)

	assert.False(t, test.controller.kick.active)
	assert.Equal(t, 255, test.pwm())
}

// A new Run starts over: the kick value is determined again.
func TestKickStart_ResetRuntimeState(t *testing.T) {
	fan := newKickTestFan(200, 32)
	test := newKickTest(t, fan, nil)
	breakaway := 116
	test.controller.breakawayPwm = &breakaway
	test.setSlowestSpeed()
	test.run(t, 20)
	require.Equal(t, 220, test.controller.kick.level)

	test.controller.resetRuntimeState()

	assert.Equal(t, kickStartState{}, test.controller.kick)
	assert.Equal(t, 0, test.controller.GetStatistics().KickStartPwm)
}

// The analysis measures the breakaway PWM value of a fan with static friction, saves it, and the daemon loads it.
func TestRunInitialization_MeasuresAndSavesTheBreakawayPwm(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	// GIVEN a fan that keeps spinning down to PWM 32, but needs 116 to start
	fan := newAnalysisTestFan(clock)
	spinning := true
	fan.rpm = func(raw int) float64 {
		if spinning && raw < 32 {
			spinning = false
		} else if !spinning && raw >= 116 {
			spinning = true
		}
		if !spinning {
			return 0
		}
		return 200 + 2.5*float64(raw)
	}
	p := newFakePersistence()
	controller := newAnalysisTestController(p, fan, clock)

	// WHEN
	_, err := controller.RunInitializationAndRestore(context.Background())

	// THEN the breakaway PWM value is within the refinement tolerance (4) above 116
	require.NoError(t, err)
	info := p.analysisInfos["fan"]
	require.NotNil(t, info.BreakawayPwm)
	assert.GreaterOrEqual(t, *info.BreakawayPwm, 116)
	assert.LessOrEqual(t, *info.BreakawayPwm, 120)

	// the daemon loads it
	daemon := newAnalysisTestController(p, fan, clock)
	daemon.loadBreakawayPwm()
	require.NotNil(t, daemon.breakawayPwm)
	assert.Equal(t, *info.BreakawayPwm, *daemon.breakawayPwm)
}

// A fan that starts at its start boundary needs only one try.
func TestMeasureBreakawayPwm_FanWithoutStaticFriction(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	analyzer := NewFanCurveAnalyzer(controller)
	start := clock.Now()

	breakaway, err := analyzer.measureBreakawayPwm(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, 60)

	require.NoError(t, err)
	require.NotNil(t, breakaway)
	assert.Equal(t, 60, *breakaway)
	assert.Equal(t, []int{0, 60}, fan.writes)
	// stop (4 readings + pause), one reading at 60
	assert.LessOrEqual(t, clock.Now().Sub(start), 5*defaultSettleSampleInterval+breakawayStandstillPause)
}

// A fan that spins at its lowest value doesn't stop: nothing to measure.
func TestMeasureBreakawayPwm_FanThatDoesNotStop(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	fan.rpm = func(raw int) float64 { return 300 }
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	analyzer := NewFanCurveAnalyzer(controller)

	// start boundary at the lowest value
	breakaway, err := analyzer.measureBreakawayPwm(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, 0)
	require.NoError(t, err)
	assert.Nil(t, breakaway)
	assert.Empty(t, fan.writes)

	// a fan that doesn't stop within the settle timeout
	breakaway, err = analyzer.measureBreakawayPwm(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, 60)
	require.NoError(t, err)
	assert.Nil(t, breakaway)
}

// kickStart.mode other than auto skips the measurement.
func TestRunInitialization_KickStartOff_SkipsTheBreakawayMeasurement(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	fan := &kickStartOffAnalysisFan{analysisTestFan: newAnalysisTestFan(clock)}
	p := newFakePersistence()
	controller := newAnalysisTestController(p, fan, clock)

	_, err := controller.RunInitializationAndRestore(context.Background())

	require.NoError(t, err)
	assert.Nil(t, p.analysisInfos["fan"].BreakawayPwm)
}

type kickStartOffAnalysisFan struct {
	*analysisTestFan
}

func (fan *kickStartOffAnalysisFan) GetConfig() configuration.FanConfig {
	config := fan.analysisTestFan.GetConfig()
	config.KickStart = &configuration.KickStartConfig{Mode: configuration.KickStartOff}
	return config
}

// loadBreakawayPwm: missing info or a record of an older version without the value means unknown.
func TestLoadBreakawayPwm_MissingValue(t *testing.T) {
	clock := util.NewFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	p := newFakePersistence()
	controller := newAnalysisTestController(p, fan, clock)

	controller.loadBreakawayPwm()
	assert.Nil(t, controller.breakawayPwm)

	delay := int64(20)
	p.analysisInfos["fan"] = persistence.FanAnalysisInfo{ReadbackDelayMillis: &delay}
	controller.loadBreakawayPwm()
	assert.Nil(t, controller.breakawayPwm)
}
