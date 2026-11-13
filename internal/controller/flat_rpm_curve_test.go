package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/markusressel/fan2go/internal/warnings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const flatRpmCurveWarning = "doesn't react to PWM"

// flatRpm is the RPM of a fan that doesn't react to PWM: it runs at the same speed for every PWM value except 0
func flatRpm(raw int) float64 {
	if raw == 0 {
		return 0
	}
	return 753
}

func flatRpmCurve() map[int]float64 {
	curve := map[int]float64{}
	for pwm := fans.MinPwmValue; pwm <= fans.MaxPwmValue; pwm++ {
		curve[pwm] = flatRpm(pwm)
	}
	return curve
}

// noManualModeFan is an analysisTestFan that can't be switched to manual mode, which makes Run return right after
// the startup (before the control loop).
type noManualModeFan struct {
	*analysisTestFan
}

var errNoManualMode = errors.New("no manual mode")

func (fan *noManualModeFan) SetControlMode(mode fans.ControlMode) error {
	if mode == fans.ControlModePWM {
		return errNoManualMode
	}
	return fan.analysisTestFan.SetControlMode(mode)
}

// seedIdentityMappings saves the fan specific mappings of a fan that accepts and reports every value
func seedIdentityMappings(p *fakePersistence) {
	setPwmToGetPwmMap, _ := util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
	p.setPwmToGetPwmMaps["fan"] = setPwmToGetPwmMap
	pwmMap := createOneToOnePwmMap()
	p.pwmMaps["fan"] = pwmMap[:]
}

func TestFanController_Run_FlatRpmCurve_WarnsOnce(t *testing.T) {
	tests := map[string]struct {
		curve        map[int]float64
		wantWarnings int
	}{
		"flat curve":   {curve: flatRpmCurve(), wantWarnings: 1},
		"normal curve": {curve: map[int]float64{0: 0, 31: 0, 32: 221, 255: 839}, wantWarnings: 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			setUpAnalysisTestConfig(t)
			defer setUpInitTestConfig(true)()

			// GIVEN: a fan with saved curve data, so Run doesn't analyze it
			fan := &noManualModeFan{newAnalysisTestFan(util.NewFakeClock(time.Unix(1000, 0)))}
			p := newFakePersistence()
			p.rpmData["fan"] = tt.curve
			seedIdentityMappings(p)
			controller := newInitTestController(p, fan)
			store := warnings.NewStore()
			// left over from an earlier Run: cleared when Run starts again
			store.Set(warnings.KindFan, "fan", warnings.CodeFlatRpmCurve, "stale")
			controller.SetWarnings(store)

			// WHEN: Run loads the curve data
			var err error
			log := captureLog(t, func() { err = runUntilError(t, controller) })

			// THEN
			assert.ErrorIs(t, err, errNoManualMode)
			assert.Equal(t, tt.wantWarnings, strings.Count(log, flatRpmCurveWarning), log)
			if tt.wantWarnings > 0 {
				fanWarnings := store.For(warnings.KindFan, "fan")
				require.Len(t, fanWarnings, 1)
				assert.Equal(t, warnings.CodeFlatRpmCurve, fanWarnings[0].Code)
				assert.Contains(t, log, "Fan fan: "+fanWarnings[0].Message)
				assert.NotEqual(t, "stale", fanWarnings[0].Message)
			} else {
				assert.Empty(t, store.All())
			}
		})
	}
}

func TestRunInitializationAndRestore_FlatRpmCurve_WarnsOnce(t *testing.T) {
	setUpAnalysisTestConfig(t)
	clock := util.NewAutoAdvancingFakeClock(time.Unix(1000, 0))

	// GIVEN: a fan that doesn't react to PWM
	fan := newAnalysisTestFan(clock)
	fan.rpm = flatRpm
	controller := newAnalysisTestController(newFakePersistence(), fan, clock)

	// WHEN
	var curve map[int]float64
	var err error
	log := captureLog(t, func() { curve, err = controller.RunInitializationAndRestore(context.Background()) })

	// THEN
	require.NoError(t, err)
	flat, _ := fans.IsFlatRpmCurveData(curve, 20)
	assert.True(t, flat, "%v", curve)
	assert.Equal(t, 1, strings.Count(log, flatRpmCurveWarning), log)
	assert.Contains(t, log, "Fan fan: RPM stays at ~753 across the whole PWM range")
	// the boundaries before config overrides show the full range, not the lowest PWM with 98% of the peak RPM
	assert.Contains(t, log, "Analysis boundaries detected (before config overrides): Start 1, Max 255")
}

// A Run that fails before it has the curve data (e.g. after a failed reload) must not keep the warning of an earlier
// Run: the condition is unknown until the curve data is loaded again.
func TestFanController_Run_ClearsFlatRpmCurveWarningOnStart(t *testing.T) {
	setUpAnalysisTestConfig(t)
	defer setUpInitTestConfig(true)()

	// GIVEN: a warning of an earlier Run, and a database that can't be opened
	fan := newAnalysisTestFan(util.NewFakeClock(time.Unix(1000, 0)))
	p := &initFailingPersistence{fakePersistence: newFakePersistence()}
	p.failInit.Store(true)
	controller := newInitTestController(p, fan)
	store := warnings.NewStore()
	store.Set(warnings.KindFan, "fan", warnings.CodeFlatRpmCurve, "stale")
	store.Set(warnings.KindFan, "other_fan", warnings.CodeFlatRpmCurve, "other fan")
	controller.SetWarnings(store)

	// WHEN
	err := controller.Run(context.Background())

	// THEN: only the warning of this fan is cleared
	require.Error(t, err)
	assert.Equal(t, []warnings.Entry{
		{Kind: warnings.KindFan, ID: "other_fan", Code: warnings.CodeFlatRpmCurve, Message: "other fan"},
	}, store.All())
}
