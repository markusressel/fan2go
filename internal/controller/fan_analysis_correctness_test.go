package controller

import (
	"context"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/quirks"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// analysisTestFan is a hwmon-like fan for analysis tests. Its RPM depends on the raw PWM value it runs at
// (rpm), it reads back readback(raw) (identity if nil), and its driver can cache the readback for cacheFor:
// a read refreshes the cache only if it is at least cacheFor old (like older versions of the out-of-tree
// nct6687 driver). Writes are recorded.
type analysisTestFan struct {
	MockFan
	clock    util.Clock
	rpm      func(raw int) float64
	readback func(raw int) int
	cacheFor time.Duration
	// raw values whose readback never matches (the fan reports -1)
	mismatchAt map[int]bool

	cacheValid   bool
	cachedPwm    int
	refreshedAt  time.Time
	noRpmSensor  bool
	configMaxPwm *int
	derivedMax   *int
	writes       []int
}

func (fan *analysisTestFan) actualReadback() int {
	raw := fan.PWM
	if fan.mismatchAt[raw] {
		return -1
	}
	if fan.readback != nil {
		return fan.readback(raw)
	}
	return raw
}

func (fan *analysisTestFan) GetPwm() (int, error) {
	if fan.cacheFor <= 0 {
		return fan.actualReadback(), nil
	}
	now := fan.clock.Now()
	if !fan.cacheValid || now.Sub(fan.refreshedAt) >= fan.cacheFor {
		fan.cachedPwm = fan.actualReadback()
		fan.refreshedAt = now
		fan.cacheValid = true
	}
	return fan.cachedPwm, nil
}

func (fan *analysisTestFan) SetPwm(pwm int) error {
	fan.writes = append(fan.writes, pwm)
	fan.PWM = pwm
	return nil
}

func (fan *analysisTestFan) GetRpm() (int, error) {
	if fan.rpm == nil {
		return 0, nil
	}
	return int(fan.rpm(fan.PWM)), nil
}

func (fan *analysisTestFan) GetConfig() configuration.FanConfig {
	config := fan.MockFan.GetConfig()
	config.HwMon = &configuration.HwMonFanConfig{}
	config.MaxPwm = fan.configMaxPwm
	return config
}

func (fan *analysisTestFan) GetMaxPwm() int {
	if fan.configMaxPwm != nil {
		return *fan.configMaxPwm
	}
	if fan.derivedMax != nil {
		return *fan.derivedMax
	}
	return fans.MaxPwmValue
}

func (fan *analysisTestFan) SetMaxPwm(pwm int, force bool) {
	fan.derivedMax = &pwm
}

func (fan *analysisTestFan) Supports(feature fans.FeatureFlag) bool {
	if feature == fans.FeatureRpmSensor {
		return !fan.noRpmSensor
	}
	return true
}

// linearRpm spins from raw PWM 60 on, 300 to 1800 RPM
func linearRpm(raw int) float64 {
	if raw < 60 {
		return 0
	}
	return 300 + 1500*float64(raw-60)/195
}

func setUpAnalysisTestConfig(t *testing.T) {
	t.Helper()
	originalConfig := configuration.CurrentConfig
	t.Cleanup(func() { configuration.CurrentConfig = originalConfig })
	configuration.CurrentConfig.FanController.PwmSetDelay = 5 * time.Millisecond
	configuration.CurrentConfig.FanResponseDelay = configuration.SecondsOrDuration(time.Second)
	configuration.CurrentConfig.Analysis.SampleCount = 1
	configuration.CurrentConfig.Analysis.SampleDelay = 0
	configuration.CurrentConfig.Analysis.CoarseStep = 16
	configuration.CurrentConfig.Analysis.SettleTimeout = 0
	configuration.CurrentConfig.MaxRpmDiffForSettledFan = 20
	configuration.CurrentConfig.RunFanInitializationInParallel = true
}

func newAnalysisTestFan(clock util.Clock) *analysisTestFan {
	return &analysisTestFan{
		MockFan: MockFan{ID: "fan", PWM: 100, ControlMode: fans.ControlModeAutomatic},
		clock:   clock,
		rpm:     linearRpm,
	}
}

func newAnalysisTestController(p persistence.Persistence, fan fans.Fan, clock util.Clock) *DefaultFanController {
	controller := newInitTestController(p, fan)
	controller.SetClock(clock)
	return controller
}

// identityMapped sets identity mappings on the controller, like computeFanSpecificMappings for a fan that accepts
// and reports every value.
func identityMapped(controller *DefaultFanController) *DefaultFanController {
	controller.pwmMapping = createOneToOnePwmMap()
	controller.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
	controller.pwmMapComputed = true
	controller.updateDistinctPwmValues()
	return controller
}

// --- maxPwm (lowest PWM with 98% of the peak) and the fail-safe speed ---

func TestAlignMaxPwmWithOutputLevels_QuantizedFan_UsesTheLevelThatReachesTheRatio(t *testing.T) {
	// GIVEN: 8 output levels of 32 written values each, reported as level * 255 / 7, measured at the first
	// value of each level and interpolated in between
	var outputLevels [256]int
	for target := 0; target <= 255; target++ {
		outputLevels[target] = (target >> 5) * 255 / 7
	}
	measured := map[int]float64{0: 0, 32: 1430, 64: 1860, 96: 2290, 128: 2710, 160: 3140, 192: 3570, 224: 4000, 255: 4000}
	curve, err := util.InterpolateLinearly(&measured, 0, 255)
	require.NoError(t, err)
	_, maxPwm := fans.ComputePwmBoundariesFromCurveData(curve, fans.MaxPwmValue)
	// the interpolated curve reaches 98% of the peak inside level 6 (192..223), which only runs at 89%
	require.Less(t, maxPwm, 224)
	require.GreaterOrEqual(t, maxPwm, 192)

	// WHEN
	aligned := alignMaxPwmWithOutputLevels(curve, maxPwm, outputLevels)

	// THEN: the first value of the top level
	assert.Equal(t, 224, aligned)
}

func TestAlignMaxPwmWithOutputLevels_ContinuousFan_KeepsMaxPwm(t *testing.T) {
	var outputLevels [256]int
	curve := map[int]float64{}
	for target := 0; target <= 255; target++ {
		outputLevels[target] = target
		curve[target] = linearRpm(target)
	}
	_, maxPwm := fans.ComputePwmBoundariesFromCurveData(curve, fans.MaxPwmValue)

	assert.Equal(t, maxPwm, alignMaxPwmWithOutputLevels(curve, maxPwm, outputLevels))
}

func TestAlignDerivedMaxPwmWithOutputLevels_ConfiguredMaxPwmWins(t *testing.T) {
	// GIVEN: a configured maxPwm inside a level that doesn't reach the ratio
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	configured := 200
	fan.configMaxPwm = &configured
	fan.speedCurve = &map[int]float64{0: 0, 199: 1000, 200: 1000, 255: 2000}
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	controller.setPwmToGetPwmMap = map[int]int{}
	for raw := 0; raw <= 255; raw++ {
		controller.setPwmToGetPwmMap[raw] = raw / 64
	}

	// WHEN
	controller.alignDerivedMaxPwmWithOutputLevels()

	// THEN
	assert.Equal(t, 200, fan.GetMaxPwm())
	assert.Nil(t, fan.derivedMax)
}

func TestSetMaxPwmForSensorFailure_UsesFullSpeedNotTheDerivedMaxPwm(t *testing.T) {
	clock := util.NewFakeClock(time.Unix(1000, 0))

	// derived maxPwm: full speed
	fan := newAnalysisTestFan(clock)
	derived := 150
	fan.derivedMax = &derived
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	controller.setMaxPwmForSensorFailure()
	assert.Equal(t, 255, fan.PWM)
	assert.Equal(t, float64(fans.MaxPwmValue), controller.maxPwmCurveValue())

	// configured maxPwm: that one
	fan = newAnalysisTestFan(clock)
	configured := 200
	fan.configMaxPwm = &configured
	fan.useUnscaledCurveValues = true
	controller = identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	controller.setMaxPwmForSensorFailure()
	assert.Equal(t, 200, fan.PWM)
	assert.Equal(t, 200.0, controller.maxPwmCurveValue())
}

// A fan that should never stop and stalls at a derived maxPwm gets the range up to full speed, instead of
// stopping the controller at the derived maxPwm.
func TestFanController_NeverStop_StalledAtDerivedMaxPwm_KeepsIncreasing(t *testing.T) {
	// GIVEN a fan that doesn't spin, with a derived maxPwm of 22
	setupStallTestConfig(t)
	s := newStallTestHwMonFan(t, 20, 22)
	config := s.fan.GetConfig()
	config.MaxPwm = nil
	s.fan.SetConfig(config)
	s.fan.SetMaxPwm(22, false)
	controller, clock := newStallTestController(t, s.fan)
	pollAndUpdateN(t, controller, clock, 1+minPwmIncreaseRpmPollingPeriods)
	pollAndUpdateN(t, controller, clock, minPwmIncreaseRpmPollingPeriods)
	assert.Equal(t, 22, s.pwm(t))

	// WHEN the fan had time to react to the derived maxPwm
	pollAndUpdateN(t, controller, clock, minPwmIncreaseRpmPollingPeriods)

	// THEN no error, the floor is raised above the derived maxPwm
	assert.Equal(t, 23, s.pwm(t))
	assert.Equal(t, 3, controller.GetStatistics().IncreasedMinPwmCount)
}

// --- a saved curve without data ---

func TestRunInitializationIfNeeded_NullCurve_AnalyzesFanWithRpmSensor(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	// GIVEN: a fan that was analyzed while its RPM sensor was missing
	fan := newAnalysisTestFan(clock)
	p := newFakePersistence()
	p.rpmData["fan"] = nil
	controller := newAnalysisTestController(p, fan, clock)

	// WHEN
	curve, err := controller.runInitializationIfNeeded(context.Background())

	// THEN
	require.NoError(t, err)
	assert.NotEmpty(t, curve)
	assert.NotEmpty(t, p.rpmData["fan"])
}

func TestRunInitializationIfNeeded_NullCurve_FanWithoutRpmSensor_UsesIt(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	fan := newAnalysisTestFan(clock)
	fan.noRpmSensor = true
	p := newFakePersistence()
	p.rpmData["fan"] = nil
	controller := newAnalysisTestController(p, fan, clock)

	curve, err := controller.runInitializationIfNeeded(context.Background())

	require.NoError(t, err)
	assert.Nil(t, curve)
	assert.Empty(t, fan.writes)
}

// --- fan init: replace the saved data only on success ---

func seedSavedAnalysisData(p *fakePersistence) {
	p.rpmData["fan"] = map[int]float64{0: 0, 100: 500, 255: 1000}
	p.setPwmToGetPwmMaps["fan"] = map[int]int{0: 0, 255: 255}
	p.pwmMaps["fan"] = make([]int, 256)
	p.analysisInfos["fan"] = persistence.FanAnalysisInfo{Quirk: "old", QuirkVersion: 1}
}

func TestRunInitializationAndRestore_Fails_KeepsTheSavedData(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	// GIVEN: saved data, and a fan that never spins, so the analysis fails
	fan := newAnalysisTestFan(clock)
	fan.rpm = func(int) float64 { return 0 }
	p := newFakePersistence()
	seedSavedAnalysisData(p)
	controller := newAnalysisTestController(p, fan, clock)

	// WHEN
	_, err := controller.RunInitializationAndRestore(context.Background())

	// THEN
	require.Error(t, err)
	assert.Equal(t, map[int]float64{0: 0, 100: 500, 255: 1000}, p.rpmData["fan"])
	assert.Equal(t, map[int]int{0: 0, 255: 255}, p.setPwmToGetPwmMaps["fan"])
	assert.Equal(t, make([]int, 256), p.pwmMaps["fan"])
	assert.Equal(t, persistence.FanAnalysisInfo{Quirk: "old", QuirkVersion: 1}, p.analysisInfos["fan"])
	mode, _ := fan.GetControlMode()
	assert.Equal(t, fans.ControlModeAutomatic, mode, "the fan is handed back")
}

func TestRunInitializationAndRestore_Succeeds_MeasuresAgainAndReplacesTheSavedData(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	fan := newAnalysisTestFan(clock)
	p := newFakePersistence()
	seedSavedAnalysisData(p)
	controller := newAnalysisTestController(p, fan, clock)

	curve, err := controller.RunInitializationAndRestore(context.Background())

	require.NoError(t, err)
	assert.Equal(t, curve, p.rpmData["fan"])
	// the set -> get map was measured again (the saved one has only 2 entries), and so was the pwm map
	assert.Len(t, p.setPwmToGetPwmMaps["fan"], 256)
	assert.Equal(t, 255, p.pwmMaps["fan"][255])
	assert.Equal(t, "", p.analysisInfos["fan"].Quirk)
	assert.False(t, controller.freshAnalysis)
}

// --- save errors ---

func TestRunInitialization_SaveRpmDataFails_ReturnsTheError(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	fan := newAnalysisTestFan(clock)
	p := &failingRpmSavePersistence{fakePersistence: newFakePersistence()}
	controller := newAnalysisTestController(p, fan, clock)

	_, err := controller.RunInitializationAndRestore(context.Background())

	assert.ErrorContains(t, err, "disk full")
	assert.NotContains(t, p.analysisInfos, "fan")
}

// --- start boundary search ---

func TestDetectStartBoundary_SkippedProbe_TriesTheNeighbour(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	// GIVEN: a fan that starts at PWM 60 and doesn't report PWM 127 (the first probe of the search) correctly
	fan := newAnalysisTestFan(clock)
	fan.mismatchAt = map[int]bool{127: true}
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	analyzer := NewFanCurveAnalyzer(controller)

	// WHEN
	_, startPwm, err := analyzer.detectStartBoundary(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, map[int]float64{})

	// THEN: not the lowest value above 127
	require.NoError(t, err)
	assert.Equal(t, 60, startPwm)
}

// --- max boundary fallbacks ---

func TestDetectMaxBoundary_NoUsableSamples_ReturnsAnError(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	fan := newAnalysisTestFan(clock)
	fan.mismatchAt = map[int]bool{}
	for raw := 0; raw <= 255; raw++ {
		fan.mismatchAt[raw] = true
	}
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))

	_, _, err := NewFanCurveAnalyzer(controller).detectMaxBoundary(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, 60, map[int]float64{})

	assert.ErrorContains(t, err, "unable to detect max boundary")
}

func TestConfirmMaxBoundary_NoConfirmedSample_UsesTheRoughCandidate(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	fan := newAnalysisTestFan(clock)
	fan.mismatchAt = map[int]bool{}
	for raw := 0; raw <= 255; raw++ {
		fan.mismatchAt[raw] = true
	}
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))

	idx, pwm, err := NewFanCurveAnalyzer(controller).confirmMaxBoundary(context.Background(), fan, controller.targetValuesWithDistinctPWMValue, 60, 200, map[int]float64{})

	require.NoError(t, err)
	assert.Equal(t, 200, idx)
	assert.Equal(t, 200, pwm)
}

// --- waiting for the fan to settle ---

// settleTestFan reports the RPM values of a function of the time since start.
type settleTestFan struct {
	MockFan
	clock util.Clock
	start time.Time
	rpmAt func(elapsed time.Duration) int
}

func (fan *settleTestFan) GetRpm() (int, error) {
	return fan.rpmAt(fan.clock.Now().Sub(fan.start)), nil
}

func newSettleTest(t *testing.T, rpmAt func(elapsed time.Duration) int) (*FanCurveAnalyzer, *settleTestFan, *util.FakeClock) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	fan := &settleTestFan{MockFan: MockFan{ID: "fan"}, clock: clock, start: clock.Now(), rpmAt: rpmAt}
	controller := &DefaultFanController{fan: fan, clock: clock}
	return NewFanCurveAnalyzer(controller), fan, clock
}

func TestWaitForFanToSettle_StableFan_SettlesBeforeTheTimeout(t *testing.T) {
	analyzer, fan, clock := newSettleTest(t, func(time.Duration) int { return 1000 })
	start := clock.Now()

	err := analyzer.waitForFanToSettle(context.Background(), fan, 30*time.Second)

	require.NoError(t, err)
	// 6 readings for the baseline, then 3 stable checks in a row (the first one counts already): 8 readings
	assert.Equal(t, 8*defaultSettleSampleInterval, clock.Now().Sub(start))
}

func TestWaitForFanToSettle_RampingFan_WaitsUntilItIsStable(t *testing.T) {
	// the fan ramps up for 10 s, then stays at 1500 RPM
	analyzer, fan, clock := newSettleTest(t, func(elapsed time.Duration) int {
		if elapsed >= 10*time.Second {
			return 1500
		}
		return 500 + int(100*elapsed.Seconds())
	})
	start := clock.Now()

	err := analyzer.waitForFanToSettle(context.Background(), fan, 60*time.Second)

	require.NoError(t, err)
	elapsed := clock.Now().Sub(start)
	assert.Greater(t, elapsed, 10*time.Second)
	assert.Less(t, elapsed, 60*time.Second)
}

func TestWaitForFanToSettle_NoisyFan_Settles(t *testing.T) {
	// the fan is at 1000 RPM with +-40 RPM of tach noise
	noise := []int{0, 37, -22, 40, -35, 12, -40, 25, -8, 33, -27, 5}
	analyzer, fan, clock := newSettleTest(t, func(elapsed time.Duration) int {
		return 1000 + noise[int(elapsed/defaultSettleSampleInterval)%len(noise)]
	})
	start := clock.Now()

	err := analyzer.waitForFanToSettle(context.Background(), fan, 30*time.Second)

	require.NoError(t, err)
	assert.Less(t, clock.Now().Sub(start), 10*time.Second)
}

func TestWaitForFanToSettle_NeverStable_GivesUpAtTheTimeout(t *testing.T) {
	// the fan keeps ramping up
	analyzer, fan, clock := newSettleTest(t, func(elapsed time.Duration) int {
		return 500 + int(100*elapsed.Seconds())
	})
	start := clock.Now()

	err := analyzer.waitForFanToSettle(context.Background(), fan, 5*time.Second)

	require.NoError(t, err)
	elapsed := clock.Now().Sub(start)
	assert.GreaterOrEqual(t, elapsed, 5*time.Second)
	assert.LessOrEqual(t, elapsed, 5*time.Second+defaultSettleSampleInterval)
}

func TestDetrendedRange(t *testing.T) {
	assert.Equal(t, 0.0, detrendedRange([]float64{5}))
	assert.InDelta(t, 0.0, detrendedRange([]float64{100, 150, 200, 250, 300}), 1e-9)
	assert.InDelta(t, 20.0, detrendedRange([]float64{10, -10, 10, -10}), 6)
}

func TestWaitForFanToSettle_Cancelled_ReturnsTheContextError(t *testing.T) {
	analyzer, fan, _ := newSettleTest(t, func(time.Duration) int { return 1000 })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := analyzer.waitForFanToSettle(ctx, fan, 30*time.Second)

	assert.ErrorIs(t, err, context.Canceled)
}

// --- pwmMap: one value per output level ---

func TestComputePwmMapAutomatically_QuantizedReadback_UsesOneValuePerLevel(t *testing.T) {
	// GIVEN: a driver that accepts every value, but only has 4 levels (reported as 0, 85, 170, 255)
	fan := &MockFan{ID: "fan"}
	controller := &DefaultFanController{fan: fan, setPwmToGetPwmMap: map[int]int{}}
	for raw := 0; raw <= 255; raw++ {
		controller.setPwmToGetPwmMap[raw] = (raw >> 6) * 85
	}

	// WHEN
	require.NoError(t, controller.computePwmMapAutomatically())
	controller.updateDistinctPwmValues()

	// THEN: 4 target ranges of equal size, each writes the value of its level that reads back as itself
	assert.Equal(t, []int{0, 64, 128, 192}, controller.targetValuesWithDistinctPWMValue)
	assert.Equal(t, 0, controller.applyPwmMapToTarget(63))
	assert.Equal(t, 85, controller.applyPwmMapToTarget(64))
	assert.Equal(t, 170, controller.applyPwmMapToTarget(128))
	assert.Equal(t, 255, controller.applyPwmMapToTarget(255))
}

func TestComputePwmMapAutomatically_SaturatingDriver_HasNoDeadTargetRange(t *testing.T) {
	// GIVEN: writes above 200 all read back as 200
	fan := &MockFan{ID: "fan"}
	controller := &DefaultFanController{fan: fan, setPwmToGetPwmMap: map[int]int{}}
	for raw := 0; raw <= 255; raw++ {
		controller.setPwmToGetPwmMap[raw] = min(raw, 200)
	}

	// WHEN
	require.NoError(t, controller.computePwmMapAutomatically())
	controller.updateDistinctPwmValues()

	// THEN: the 201 levels are spread over all target values, the top target selects 200
	assert.Len(t, controller.targetValuesWithDistinctPWMValue, 201)
	assert.Equal(t, 200, controller.applyPwmMapToTarget(255))
	assert.Less(t, controller.applyPwmMapToTarget(250), 200)
}

func TestComputePwmMapAutomatically_UnusableReadback_UsesEveryAcceptedValue(t *testing.T) {
	tests := map[string]func(raw int) int{
		"decreasing": func(raw int) int { return 255 - raw },
		"constant":   func(raw int) int { return 0 },
	}
	for name, readback := range tests {
		t.Run(name, func(t *testing.T) {
			fan := &MockFan{ID: "fan"}
			controller := &DefaultFanController{fan: fan, setPwmToGetPwmMap: map[int]int{}}
			for raw := 0; raw <= 255; raw++ {
				controller.setPwmToGetPwmMap[raw] = readback(raw)
			}

			require.NoError(t, controller.computePwmMapAutomatically())

			assert.Equal(t, createOneToOnePwmMap(), controller.pwmMapping)
		})
	}
}

// nvidiaConfigFan is a MockFan configured as an nvidia fan.
type nvidiaConfigFan struct {
	MockFan
}

func (fan *nvidiaConfigFan) GetConfig() configuration.FanConfig {
	config := fan.MockFan.GetConfig()
	config.Nvidia = &configuration.NvidiaFanConfig{}
	return config
}

func TestComputePwmMapAutomatically_NvidiaFan_UsesEveryAcceptedValue(t *testing.T) {
	// GIVEN: an nvidia fan reports percent: writes above 100 read back as 100
	fan := &nvidiaConfigFan{MockFan: MockFan{ID: "fan"}}
	controller := &DefaultFanController{fan: fan, setPwmToGetPwmMap: map[int]int{}}
	for raw := 0; raw <= 255; raw++ {
		controller.setPwmToGetPwmMap[raw] = min(raw, 100)
	}

	// WHEN
	require.NoError(t, controller.computePwmMapAutomatically())

	// THEN: its PWM boundaries are percent values of the identity mapping, so that stays
	assert.Equal(t, createOneToOnePwmMap(), controller.pwmMapping)
}

func TestComputePwmMapAutomatically_KnownReadbackFormula_UsesItsOutputLevels(t *testing.T) {
	// GIVEN: the readback formula of the Corsair Commander Pro
	entry := quirks.Lookup("corsaircpro", "")
	require.NotNil(t, entry)
	fan := &MockFan{ID: "fan"}
	controller := &DefaultFanController{fan: fan, setPwmToGetPwmMap: entry.SetPwmToGetPwmMap()}

	// WHEN
	require.NoError(t, controller.computePwmMapAutomatically())
	controller.updateDistinctPwmValues()

	// THEN: one target range per output level, and each writes a value that reads back as itself
	assert.Len(t, controller.targetValuesWithDistinctPWMValue, len(entry.OutputLevels()))
	for _, target := range controller.targetValuesWithDistinctPWMValue {
		raw := controller.applyPwmMapToTarget(target)
		assert.Equal(t, raw, entry.Readback(raw))
	}
	assert.Equal(t, 0, controller.applyPwmMapToTarget(0))
	assert.Equal(t, 255, controller.applyPwmMapToTarget(255))
}

// --- readback delay ---

func TestComputeSetPwmToGetPwmMapAutomatically_CachedReadback_MeasuresTheRightMap(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	// GIVEN: a driver that caches the readback for 300 ms
	fan := newAnalysisTestFan(clock)
	fan.cacheFor = 300 * time.Millisecond
	controller := newAnalysisTestController(newFakePersistence(), fan, clock)

	// WHEN
	require.NoError(t, controller.computeSetPwmToGetPwmMapAutomatically(context.Background()))

	// THEN: every value reads back as itself, not as the value written before it
	for raw := 0; raw <= 255; raw++ {
		assert.Equal(t, raw, controller.setPwmToGetPwmMap[raw], "raw %d", raw)
	}
	require.NotNil(t, controller.observedReadbackDelay)
	assert.GreaterOrEqual(t, *controller.observedReadbackDelay, 295*time.Millisecond)
	assert.LessOrEqual(t, *controller.observedReadbackDelay, 300*time.Millisecond)
}

func TestComputeSetPwmToGetPwmMapAutomatically_ReadbackNeverChanges_NoDelayMeasured(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	fan := newAnalysisTestFan(clock)
	fan.readback = func(int) int { return 42 }
	controller := newAnalysisTestController(newFakePersistence(), fan, clock)
	start := clock.Now()

	require.NoError(t, controller.computeSetPwmToGetPwmMapAutomatically(context.Background()))

	assert.Nil(t, controller.observedReadbackDelay)
	// 4 probe writes of up to maxReadbackDelay each, then one pwmSetDelay per value
	assert.LessOrEqual(t, clock.Now().Sub(start), 4*(maxReadbackDelay+readbackPollInterval)+256*5*time.Millisecond)
}

func TestRunInitialization_CachedReadback_SavesTheReadbackDelay(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	fan := newAnalysisTestFan(clock)
	fan.cacheFor = 300 * time.Millisecond
	p := newFakePersistence()
	controller := newAnalysisTestController(p, fan, clock)

	_, err := controller.RunInitializationAndRestore(context.Background())

	require.NoError(t, err)
	info := p.analysisInfos["fan"]
	require.NotNil(t, info.ReadbackDelayMillis)
	assert.Equal(t, int64(300), *info.ReadbackDelayMillis)
	assert.Equal(t, 300*time.Millisecond, controller.readbackDelay)

	// the daemon loads it
	daemon := newAnalysisTestController(p, fan, clock)
	daemon.loadReadbackDelay()
	assert.Equal(t, 300*time.Millisecond, daemon.readbackDelay)
}

func TestLoadReadbackDelay_RecordWithoutDelay_NotMeasured(t *testing.T) {
	clock := util.NewFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	p := newFakePersistence()
	// a record written before the readback delay was measured
	p.analysisInfos["fan"] = persistence.FanAnalysisInfo{Quirk: "corsaircpro", QuirkVersion: 1}
	controller := newAnalysisTestController(p, fan, clock)
	controller.readbackDelay = time.Second

	controller.loadReadbackDelay()

	assert.Equal(t, time.Duration(0), controller.readbackDelay)
	assert.False(t, controller.readbackMayBeStale(clock.Now()))
}

// newCachedReadbackController returns a controller with a measured readback delay of 1 s, for a fan that
// caches its readback for 1 s and currently runs at (and reports) raw PWM 100.
func newCachedReadbackController(t *testing.T, readbackDelay time.Duration) (*DefaultFanController, *analysisTestFan, *util.FakeClock) {
	setUpAnalysisTestConfig(t)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	fan.ControlMode = fans.ControlModePWM
	fan.cacheFor = time.Second
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	controller.readbackDelay = readbackDelay
	_, _ = fan.GetPwm() // the cache holds 100
	return controller, fan, clock
}

func TestSetTargetPwm_CachedReadback_ABAWriteIsNotLost(t *testing.T) {
	// GIVEN
	controller, fan, clock := newCachedReadbackController(t, time.Second)

	// WHEN: B, then A again within the cache period, while the readback still shows A
	clock.Advance(10 * time.Millisecond)
	require.NoError(t, controller.setTargetPwm(150))
	clock.Advance(10 * time.Millisecond)
	require.NoError(t, controller.setTargetPwm(100))

	// THEN
	assert.Equal(t, []int{150, 100}, fan.writes)
	assert.Equal(t, 100, fan.PWM)

	// and the same value again within the delay isn't written again
	clock.Advance(10 * time.Millisecond)
	require.NoError(t, controller.setTargetPwm(100))
	assert.Equal(t, []int{150, 100}, fan.writes)
}

func TestSetTargetPwm_NoMeasuredReadbackDelay_ComparesTheReadback(t *testing.T) {
	// without a measured delay, the readback decides, like before (and the A/B/A write is lost)
	controller, fan, clock := newCachedReadbackController(t, 0)

	clock.Advance(10 * time.Millisecond)
	require.NoError(t, controller.setTargetPwm(150))
	clock.Advance(10 * time.Millisecond)
	require.NoError(t, controller.setTargetPwm(100))

	assert.Equal(t, []int{150}, fan.writes)
}

func TestEnsureNoThirdPartyIsMessingWithUs_CachedReadback_NoWarningWithinTheDelay(t *testing.T) {
	// GIVEN
	controller, _, clock := newCachedReadbackController(t, time.Second)
	clock.Advance(10 * time.Millisecond)
	require.NoError(t, controller.setTargetPwm(150))

	// WHEN: checked while the readback still shows the old value, and after it was refreshed
	clock.Advance(200 * time.Millisecond)
	controller.ensureNoThirdPartyIsMessingWithUs()
	clock.Advance(time.Second)
	controller.ensureNoThirdPartyIsMessingWithUs()

	// THEN
	assert.Equal(t, 0, controller.GetStatistics().UnexpectedPwmValueCount)
}

func TestEnsureNoThirdPartyIsMessingWithUs_CachedReadback_DetectsChangesAfterTheDelay(t *testing.T) {
	// GIVEN
	controller, fan, clock := newCachedReadbackController(t, time.Second)
	require.NoError(t, controller.setTargetPwm(150))
	clock.Advance(2 * time.Second)

	// WHEN: a third party writes another value
	fan.PWM = 80
	clock.Advance(time.Second)
	controller.ensureNoThirdPartyIsMessingWithUs()

	// THEN
	assert.Equal(t, 1, controller.GetStatistics().UnexpectedPwmValueCount)
}

// --- measureAtPwm with a lagging readback ---

func TestMeasureAtPwm_CachedReadback_RecordsTheObservedDelay(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	fan := newAnalysisTestFan(clock)
	fan.cacheFor = 400 * time.Millisecond
	controller := identityMapped(newAnalysisTestController(newFakePersistence(), fan, clock))
	_, _ = fan.GetPwm()

	rpm, err := NewFanCurveAnalyzer(controller).measureAtPwm(context.Background(), fan, 200, 0)

	require.NoError(t, err)
	assert.Equal(t, float64(int(linearRpm(200))), rpm)
	require.NotNil(t, controller.observedReadbackDelay)
	assert.Equal(t, 400*time.Millisecond, *controller.observedReadbackDelay)
}
