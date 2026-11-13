package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staticFrictionRpm returns an RPM function for an analysisTestFan that keeps spinning down to keepSpinning and
// starts from standstill at breakaway or more, spinUp after the fan got it (e.g. a locked-rotor restart cycle). The
// RPM changes by 1 on every read while spinning (tach noise).
func staticFrictionRpm(clock util.Clock, keepSpinning int, breakaway int, spinUp time.Duration) func(raw int) float64 {
	spinning := true
	var startAt time.Time
	noise := 0.0
	return func(raw int) float64 {
		now := clock.Now()
		switch {
		case spinning && raw < keepSpinning:
			spinning = false
		case !spinning && raw >= breakaway:
			if startAt.IsZero() {
				startAt = now.Add(spinUp)
			}
			if !now.Before(startAt) {
				spinning = true
				startAt = time.Time{}
			}
		case !spinning:
			startAt = time.Time{}
		}
		if !spinning {
			return 0
		}
		noise = 1 - noise
		return 200 + 2.5*float64(raw) + noise
	}
}

// The analysis measures the breakaway PWM value of a fan with static friction, the tach interval and the spin-up
// delay, saves them, and the daemon loads them.
func TestRunInitialization_MeasuresAndSavesTheBreakawayPwm(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	// GIVEN a fan that keeps spinning down to PWM 32, but needs 116 to start, after 1s
	fan := newAnalysisTestFan(clock)
	fan.rpm = staticFrictionRpm(clock, 32, 116, time.Second)
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
	require.NotNil(t, info.SpinUpMillis)
	assert.InDelta(t, 1000, *info.SpinUpMillis, 150)
	// every read changes the value: the poll interval
	require.NotNil(t, info.TachIntervalMillis)
	assert.Equal(t, breakawayPollInterval.Milliseconds(), *info.TachIntervalMillis)
	// the fan stops at once: the first reading after the write shows it
	require.NotNil(t, info.CoastDownMillis)
	assert.Equal(t, breakawayPollInterval.Milliseconds(), *info.CoastDownMillis)

	// the daemon loads it
	daemon := newAnalysisTestController(p, fan, clock)
	daemon.loadKickStartInfo()
	require.NotNil(t, daemon.breakawayPwm)
	assert.Equal(t, *info.BreakawayPwm, *daemon.breakawayPwm)
	assert.Equal(t, time.Duration(*info.SpinUpMillis)*time.Millisecond, daemon.spinUpDelay)
	assert.Equal(t, breakawayPollInterval, daemon.tachInterval)
	assert.Equal(t, breakawayPollInterval, daemon.coastDown)
}

// Saved timing values above their limits (e.g. corrupt data) are capped.
func TestLoadKickStartInfo_CapsTimingValues(t *testing.T) {
	clock := util.NewFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	p := newFakePersistence()
	controller := newAnalysisTestController(p, fan, clock)
	long := int64(time.Hour / time.Millisecond)
	p.analysisInfos["fan"] = persistence.FanAnalysisInfo{TachIntervalMillis: &long, SpinUpMillis: &long, CoastDownMillis: &long}

	controller.loadKickStartInfo()

	assert.Equal(t, maxKickTimingValue, controller.tachInterval)
	assert.Equal(t, maxKickTimingValue, controller.spinUpDelay)
	assert.Equal(t, maxCoastDown, controller.coastDown)
}

// A driver that reports a written PWM value only after a while: each try starts once the fan reports the value, so
// the result isn't too high.
func TestMeasureBreakawayPwm_SlowReadback(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	fan.cacheFor = time.Second
	fan.rpm = staticFrictionRpm(clock, 32, 116, 4*time.Second)
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	analyzer := NewFanCurveAnalyzer(controller)

	result, err := analyzer.measureBreakawayPwm(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, 40)

	require.NoError(t, err)
	require.NotNil(t, result.Pwm)
	assert.GreaterOrEqual(t, *result.Pwm, 116)
	assert.LessOrEqual(t, *result.Pwm, 120)
}

// A BLDC fan with locked-rotor protection only tries to start every few seconds: a start 3.5s after the write is
// within the try window, and the result isn't too high.
func TestMeasureBreakawayPwm_LockedRotorRetry(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	fan.rpm = staticFrictionRpm(clock, 32, 116, 3500*time.Millisecond)
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	analyzer := NewFanCurveAnalyzer(controller)

	result, err := analyzer.measureBreakawayPwm(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, 40)

	require.NoError(t, err)
	require.NotNil(t, result.Pwm)
	assert.GreaterOrEqual(t, *result.Pwm, 116)
	assert.LessOrEqual(t, *result.Pwm, 120)
	require.NotNil(t, result.SpinUp)
	assert.InDelta(t, 3500, result.SpinUp.Milliseconds(), 150)
}

// A single garbage reading that shows the stopped fan spinning (e.g. another program reads the chip at the same time)
// doesn't count as a start.
func TestMeasureBreakawayPwm_OneOffSpinningGlitch(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	real := staticFrictionRpm(clock, 32, 116, time.Second)
	glitched := false
	fan.rpm = func(raw int) float64 {
		rpm := real(raw)
		if raw == 40 && rpm == 0 && !glitched {
			glitched = true
			return 830
		}
		return rpm
	}
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	analyzer := NewFanCurveAnalyzer(controller)

	result, err := analyzer.measureBreakawayPwm(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, 40)

	require.NoError(t, err)
	require.True(t, glitched)
	require.NotNil(t, result.Pwm)
	assert.GreaterOrEqual(t, *result.Pwm, 116)
	assert.LessOrEqual(t, *result.Pwm, 120)
}

// A fan that starts at its start boundary needs only one try.
func TestMeasureBreakawayPwm_FanWithoutStaticFriction(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	analyzer := NewFanCurveAnalyzer(controller)

	result, err := analyzer.measureBreakawayPwm(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, 60)

	require.NoError(t, err)
	require.NotNil(t, result.Pwm)
	assert.Equal(t, 60, *result.Pwm)
	assert.Equal(t, []int{0, 60}, fan.writes)
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
	result, err := analyzer.measureBreakawayPwm(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, 0)
	require.NoError(t, err)
	assert.Nil(t, result.Pwm)
	assert.Empty(t, fan.writes)

	// a fan that doesn't stop within the settle timeout
	result, err = analyzer.measureBreakawayPwm(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, 60)
	require.NoError(t, err)
	assert.Nil(t, result.Pwm)
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
	assert.Nil(t, p.analysisInfos["fan"].SpinUpMillis)
}

type kickStartOffAnalysisFan struct {
	*analysisTestFan
}

func (fan *kickStartOffAnalysisFan) GetConfig() configuration.FanConfig {
	config := fan.analysisTestFan.GetConfig()
	config.KickStart = &configuration.KickStartConfig{Mode: configuration.KickStartOff}
	return config
}

// loadKickStartInfo: missing info or a record of an older version without the values means unknown.
func TestLoadKickStartInfo_MissingValues(t *testing.T) {
	clock := util.NewFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	p := newFakePersistence()
	controller := newAnalysisTestController(p, fan, clock)

	controller.loadKickStartInfo()
	assert.Nil(t, controller.breakawayPwm)

	delay := int64(20)
	p.analysisInfos["fan"] = persistence.FanAnalysisInfo{ReadbackDelayMillis: &delay}
	controller.loadKickStartInfo()
	assert.Nil(t, controller.breakawayPwm)
	assert.Zero(t, controller.tachInterval)
	assert.Zero(t, controller.spinUpDelay)
	assert.Zero(t, controller.coastDown)
	assert.Equal(t, defaultCoastDown, controller.kickStartCoastAllowance())
	// the defaults: 0 + 2s + 1s, plus 2 tach intervals
	assert.Equal(t, 5*time.Second, controller.kickStartTimeout(configuration.KickStartConfig{}))
}

// scriptedRpmFan is an analysisTestFan whose RPM readings follow a script: read(n, now) returns the RPM of the n-th
// reading (from 1), or an error. After maxReads readings it cancels the context of the test (a measurement that
// doesn't end).
type scriptedRpmFan struct {
	*analysisTestFan
	read     func(n int, now time.Time) (int, error)
	reads    int
	maxReads int
	cancel   context.CancelFunc
}

func (fan *scriptedRpmFan) GetRpm() (int, error) {
	fan.reads++
	if fan.reads > fan.maxReads {
		fan.cancel()
	}
	return fan.read(fan.reads, fan.clock.Now())
}

func newScriptedTry(t *testing.T, read func(n int, now time.Time) (int, error)) (*breakawayMeasurement, *scriptedRpmFan, context.Context, util.Clock) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	fan := &scriptedRpmFan{analysisTestFan: newAnalysisTestFan(clock), read: read, maxReads: 10000, cancel: cancel}
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	return &breakawayMeasurement{analyzer: NewFanCurveAnalyzer(controller), fan: fan}, fan, ctx, clock
}

// A spinning reading at the end of a try, followed by failing readings: the try ends (no endless polling), not started.
func TestMeasureBreakawayPwm_ReadErrorsAfterASpinningReading_TryEnds(t *testing.T) {
	var start time.Time
	m, fan, ctx, clock := newScriptedTry(t, func(n int, now time.Time) (int, error) {
		switch {
		case now.Sub(start) < minBreakawayTryWindow-100*time.Millisecond:
			return 0, nil
		case now.Sub(start) < minBreakawayTryWindow:
			return 800, nil
		default:
			return 0, fmt.Errorf("read failed")
		}
	})
	start = clock.Now()

	started, _, err := m.tryStart(ctx, 120)

	require.NoError(t, err)
	assert.False(t, started)
	assert.Less(t, fan.reads, 200)
	// the window, plus at most 2 tach intervals for the confirmation
	assert.LessOrEqual(t, clock.Now().Sub(start), minBreakawayTryWindow+2*defaultTachInterval+time.Second)
}

// Spinning readings around a failed one aren't 2 in a row.
func TestMeasureBreakawayPwm_SpinErrorSpin_NotStarted(t *testing.T) {
	m, _, ctx, _ := newScriptedTry(t, func(n int, now time.Time) (int, error) {
		switch n % 4 {
		case 1:
			return 800, nil
		case 2:
			return 0, fmt.Errorf("read failed")
		case 3:
			return 800, nil
		default:
			return 0, nil
		}
	})

	started, _, err := m.tryStart(ctx, 120)

	require.NoError(t, err)
	assert.False(t, started)
}

// Failing readings while the fan stops don't count as standstill, and don't end the measurement with an error.
func TestMeasureBreakawayPwm_ReadErrorsWhileStopping(t *testing.T) {
	m, _, ctx, _ := newScriptedTry(t, func(n int, now time.Time) (int, error) {
		if n%2 == 0 {
			return 0, fmt.Errorf("read failed")
		}
		return 0, nil
	})

	stopped, err := m.stopAndWaitForStandstill(ctx, 0)

	require.NoError(t, err)
	// never 3 standstill readings in a row
	assert.False(t, stopped)
}
