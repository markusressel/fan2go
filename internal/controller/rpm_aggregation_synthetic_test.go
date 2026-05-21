package controller

import (
	"math"
	"testing"

	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
)

type syntheticAggregationScenario struct {
	name      string
	trueRPM   float64
	rpmSeries []float64
}

func syntheticAggregationScenarios() []syntheticAggregationScenario {
	return []syntheticAggregationScenario{
		{
			name:      "steady_noise",
			trueRPM:   1000,
			rpmSeries: []float64{1000, 980, 1020, 995, 1010, 990, 1005, 1000},
		},
		{
			name:      "single_high_spike",
			trueRPM:   1000,
			rpmSeries: []float64{1000, 1005, 3200, 995, 1002, 998, 1001, 1000},
		},
		{
			name:      "single_low_drop",
			trueRPM:   1000,
			rpmSeries: []float64{1000, 995, 50, 1002, 998, 1001, 997, 1000},
		},
		{
			name:      "rising_trend",
			trueRPM:   1110,
			rpmSeries: []float64{900, 930, 960, 990, 1020, 1050, 1080, 1110},
		},
		{
			name:      "falling_trend",
			trueRPM:   890,
			rpmSeries: []float64{1100, 1070, 1040, 1010, 980, 950, 920, 890},
		},
	}
}

func TestSyntheticCurveSmoothing_PreservesBoundaryDetection(t *testing.T) {
	rawCurve := map[int]float64{}
	for pwm := 0; pwm <= 255; pwm++ {
		rawCurve[pwm] = 0
	}
	for pwm := 60; pwm <= 255; pwm++ {
		// Slow rise with a tiny but real increase at 255.
		rawCurve[pwm] = 800 + float64(pwm-60)*3
	}
	rawCurve[255] += 5

	startRaw, maxRaw := fans.ComputePwmBoundariesFromCurveData(rawCurve, fans.MaxPwmValue)
	assert.Equal(t, 60, startRaw)
	assert.Equal(t, 255, maxRaw)

	smoothed := util.SmoothMapValuesKalman(rawCurve, startRaw+1, maxRaw-1, util.DefaultKalmanConfig)
	startSmoothed, maxSmoothed := fans.ComputePwmBoundariesFromCurveData(smoothed, fans.MaxPwmValue)

	assert.Equal(t, startRaw, startSmoothed)
	assert.Equal(t, maxRaw, maxSmoothed)
}

func TestSyntheticCurveSmoothing_DoesNotAlterOutsideInteriorRange(t *testing.T) {
	curve := map[int]float64{}
	for pwm := 0; pwm <= 255; pwm++ {
		curve[pwm] = math.Max(0, float64(pwm-40)*10)
	}

	startPwm := 45
	maxPwm := 250
	smoothed := util.SmoothMapValuesKalman(curve, startPwm+1, maxPwm-1, util.DefaultKalmanConfig)

	// Values outside interior smoothing range stay untouched.
	assert.Equal(t, curve[startPwm], smoothed[startPwm])
	assert.Equal(t, curve[maxPwm], smoothed[maxPwm])
	assert.Equal(t, curve[startPwm-1], smoothed[startPwm-1])
	assert.Equal(t, curve[maxPwm+1], smoothed[maxPwm+1])
}
