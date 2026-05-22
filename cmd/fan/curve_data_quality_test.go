package fan

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnalyzeCurveDataQuality_CompleteCurveHasNoWarnings(t *testing.T) {
	curve := map[int]float64{}
	for pwm := 0; pwm <= 255; pwm++ {
		curve[pwm] = float64(pwm)
	}

	warnings := analyzeCurveDataQuality(curve)
	assert.Empty(t, warnings)
}

func TestAnalyzeCurveDataQuality_DetectsMissingAnchorsAndGaps(t *testing.T) {
	curve := map[int]float64{
		10: 0,
		11: 0,
		15: 100,
		18: 200,
	}

	warnings := analyzeCurveDataQuality(curve)

	assert.Contains(t, warnings, "missing PWM 0 anchor in persisted curve data")
	assert.Contains(t, warnings, "missing PWM 255 anchor in persisted curve data")
	assert.Contains(t, warnings, "curve domain is truncated to [10..18]")
	assert.Contains(t, warnings, "curve has 5 missing PWM keys in [10..18]")
}
