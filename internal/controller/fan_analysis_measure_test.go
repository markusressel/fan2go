package controller

import (
	"context"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
)

// mockFanLaggingPwmReadback is a fan whose reported PWM converges to the requested value only after lag has passed.
type mockFanLaggingPwmReadback struct {
	MockFan
	clock        util.Clock
	lag          time.Duration
	targetPwm    int
	reportedPwm  int
	lastChangeAt time.Time
}

func (fan *mockFanLaggingPwmReadback) SetPwm(pwm int) error {
	if pwm != fan.targetPwm {
		fan.reportedPwm = fan.targetPwm
		fan.targetPwm = pwm
		fan.lastChangeAt = fan.clock.Now()
	}
	return nil
}

func (fan *mockFanLaggingPwmReadback) GetPwm() (int, error) {
	if fan.clock.Now().Sub(fan.lastChangeAt) >= fan.lag {
		return fan.targetPwm, nil
	}
	return fan.reportedPwm, nil
}

func TestMeasureAtPwm_LaggingReadback_HonorsFanResponseDelay(t *testing.T) {
	// GIVEN
	originalConfig := configuration.CurrentConfig
	defer func() {
		configuration.CurrentConfig = originalConfig
	}()
	configuration.CurrentConfig.FanController.PwmSetDelay = 1 * time.Millisecond
	configuration.CurrentConfig.FanResponseDelay = configuration.SecondsOrDuration(time.Second)
	configuration.CurrentConfig.Analysis.SampleCount = 1
	configuration.CurrentConfig.Analysis.SampleDelay = 0

	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))
	fan := &mockFanLaggingPwmReadback{
		MockFan: MockFan{
			ID:  "fan",
			RPM: 900,
		},
		clock: clock,
		lag:   300 * time.Millisecond,
	}
	controller := &DefaultFanController{
		fan:   fan,
		clock: clock,
	}
	for i := 0; i < 256; i++ {
		controller.pwmMapping[i] = i
	}
	analyzer := NewFanCurveAnalyzer(controller)

	// WHEN
	rpm, err := analyzer.measureAtPwm(context.Background(), fan, 128, 0)

	// THEN: after FanResponseDelay the reported PWM converges and the RPM is measured
	assert.NoError(t, err)
	assert.Equal(t, 900.0, rpm, "measureAtPwm should wait FanResponseDelay for the reported PWM to converge instead of skipping the sample")
}

// mockFanRecordingRawPwm is a fan that records every raw PWM value written to it
// and whose RPM depends on the raw PWM it currently runs at.
type mockFanRecordingRawPwm struct {
	MockFan
	rpmPerRawPwm int
	writes       []int
}

func (fan *mockFanRecordingRawPwm) SetPwm(pwm int) error {
	fan.writes = append(fan.writes, pwm)
	fan.PWM = pwm
	return nil
}

func (fan *mockFanRecordingRawPwm) GetRpm() (int, error) {
	return fan.PWM * fan.rpmPerRawPwm, nil
}

// setupNonIdempotentPwmMapController creates a controller for a fan with the user-defined,
// non-idempotent pwmMap {0: 0, 128: 3, 255: 6} (expanded with step interpolation, so
// pwmMap[128] = 3 but pwmMap[3] = 0) and an identity setPwmToGetPwmMap.
func setupNonIdempotentPwmMapController(t *testing.T) (*mockFanRecordingRawPwm, *DefaultFanController) {
	t.Helper()
	originalConfig := configuration.CurrentConfig
	t.Cleanup(func() {
		configuration.CurrentConfig = originalConfig
	})
	configuration.CurrentConfig.FanController.PwmSetDelay = 0
	configuration.CurrentConfig.FanResponseDelay = 0
	configuration.CurrentConfig.Analysis.SampleCount = 1
	configuration.CurrentConfig.Analysis.SampleDelay = 0
	configuration.CurrentConfig.Analysis.CoarseStep = 1
	configuration.CurrentConfig.Analysis.SettleTimeout = 0

	pwmMapValues := configuration.PwmMapValuesConfig(map[int]int{0: 0, 128: 3, 255: 6})
	fan := &mockFanRecordingRawPwm{
		MockFan: MockFan{
			ID:     "fan",
			MaxPWM: 255,
			PwmMap: &configuration.PwmMapConfig{Values: &pwmMapValues},
			SetPwmToGetPwmMap: &configuration.SetPwmToGetPwmMapConfig{
				Identity: &configuration.SetPwmToGetPwmMapIdentityConfig{},
			},
		},
		rpmPerRawPwm: 200,
	}
	controller := &DefaultFanController{
		persistence: mockPersistence{},
		fan:         fan,
	}
	err := controller.computeFanSpecificMappings(context.Background())
	assert.NoError(t, err)
	assert.Equal(t, 3, controller.applyPwmMapToTarget(128))
	assert.Equal(t, 0, controller.applyPwmMapToTarget(3))
	assert.Equal(t, []int{0, 128, 255}, controller.targetValuesWithDistinctPWMValue)
	return fan, controller
}

func TestMeasureAtPwm_NonIdempotentPwmMap_AppliesPwmMapOnlyOnce(t *testing.T) {
	// GIVEN
	fan, controller := setupNonIdempotentPwmMapController(t)
	analyzer := NewFanCurveAnalyzer(controller)

	// WHEN
	rpm, err := analyzer.measureAtPwm(context.Background(), fan, 128, 0)

	// THEN: target 128 is written as raw pwmMap[128] = 3, not as pwmMap[pwmMap[128]] = 0
	assert.NoError(t, err)
	assert.Equal(t, []int{3}, fan.writes)
	assert.Equal(t, 3*200.0, rpm)
	// lastTarget stays in the target domain (pre-pwmMap)
	if assert.NotNil(t, controller.lastTarget) {
		assert.Equal(t, 128, *controller.lastTarget)
	}
}

// mockFanRecordingRawPwmWithoutPwmSensor is a mockFanRecordingRawPwm that can't read back its PWM value,
// so the controller has to derive the current raw PWM from lastTarget.
type mockFanRecordingRawPwmWithoutPwmSensor struct {
	*mockFanRecordingRawPwm
}

func (fan *mockFanRecordingRawPwmWithoutPwmSensor) Supports(feature fans.FeatureFlag) bool {
	return feature != fans.FeaturePwmSensor
}

func TestMeasureAtPwm_NonIdempotentPwmMap_WithoutPwmSensor_AppliesPwmMapOnlyOnce(t *testing.T) {
	// GIVEN
	recordingFan, controller := setupNonIdempotentPwmMapController(t)
	fan := &mockFanRecordingRawPwmWithoutPwmSensor{mockFanRecordingRawPwm: recordingFan}
	controller.fan = fan
	analyzer := NewFanCurveAnalyzer(controller)

	// WHEN
	rpm, err := analyzer.measureAtPwm(context.Background(), fan, 128, 0)

	// THEN: target 128 is written as raw 3, and getPwm() derives raw 3 from lastTarget
	// (pwmMap[lastTarget], not pwmMap[pwmMap[lastTarget]] = 0)
	assert.NoError(t, err)
	assert.Equal(t, []int{3}, fan.writes)
	assert.Equal(t, 3*200.0, rpm)
	currentPwm, err := controller.getPwm()
	assert.NoError(t, err)
	assert.Equal(t, 3, currentPwm)
}

func TestRunInitializationSequence_NonIdempotentPwmMap_DetectsBoundariesInTargetDomain(t *testing.T) {
	// GIVEN
	fan, controller := setupNonIdempotentPwmMapController(t)
	analyzer := NewFanCurveAnalyzer(controller)

	// WHEN
	curve, err := analyzer.RunInitializationSequence(context.Background())

	// THEN: only raw values from the pwmMap's value range are written
	assert.NoError(t, err)
	assert.Contains(t, fan.writes, 3)
	assert.Contains(t, fan.writes, 6)
	for _, raw := range fan.writes {
		assert.Contains(t, []int{0, 3, 6}, raw)
	}

	// the curve is keyed by target values: target 128 (raw 3) is the first spinning point
	startPwm, maxPwm := fans.ComputePwmBoundariesFromCurveData(curve, fans.MaxPwmValue)
	assert.Equal(t, 128, startPwm)
	assert.Equal(t, 255, maxPwm)
	assert.Equal(t, 0.0, curve[127])
	assert.Equal(t, 3*200.0, curve[128])
	assert.Equal(t, 6*200.0, curve[255])
}
