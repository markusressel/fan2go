package controller

import (
	"testing"

	"github.com/markusressel/fan2go/internal/fans"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRpmCurveMeasurementCleanup_UsesSpinThresholdAndFillsEndpoints(t *testing.T) {
	analyzer := &FanCurveAnalyzer{}

	curveData := map[int]float64{
		20:  10,
		30:  40,
		40:  55,
		120: 800,
		200: 1200,
	}

	cleaned, err := analyzer.rpmCurveMeasurementCleanup(curveData)
	require.NoError(t, err)

	assert.Equal(t, 0.0, cleaned[fans.MinPwmValue])
	assert.Equal(t, 0.0, cleaned[20])
	assert.Equal(t, 0.0, cleaned[30])
	assert.GreaterOrEqual(t, cleaned[40], 50.0)
	assert.Contains(t, cleaned, fans.MaxPwmValue)

	prev := cleaned[40]
	for pwm := 41; pwm <= fans.MaxPwmValue; pwm++ {
		assert.GreaterOrEqual(t, cleaned[pwm], prev)
		prev = cleaned[pwm]
	}
}
