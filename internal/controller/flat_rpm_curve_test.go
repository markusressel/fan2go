package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
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

			// WHEN: Run loads the curve data
			var err error
			log := captureLog(t, func() { err = runUntilError(t, controller) })

			// THEN
			assert.ErrorIs(t, err, errNoManualMode)
			assert.Equal(t, tt.wantWarnings, strings.Count(log, flatRpmCurveWarning), log)
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
}
