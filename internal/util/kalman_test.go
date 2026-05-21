package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKalmanFilter_ConvergesOnNoisyConstantSignal(t *testing.T) {
	samples := []float64{1000, 980, 1020, 995, 1010, 990, 1005, 1000}

	filter := NewKalmanFilter(DefaultKalmanConfig, samples[0])
	estimate := samples[0]
	for _, sample := range samples {
		estimate = filter.Update(sample)
	}

	assert.InDelta(t, 1000.0, estimate, 20.0)
}

func TestKalmanFilter_DampensSingleOutlierSpike(t *testing.T) {
	samples := []float64{1000, 1005, 3200, 995, 1002}

	filter := NewKalmanFilter(DefaultKalmanConfig, samples[0])
	afterSpike := 0.0
	for i, sample := range samples {
		estimate := filter.Update(sample)
		if i == 2 {
			afterSpike = estimate
		}
	}

	assert.Less(t, afterSpike, 2000.0)
}

func TestKalmanFilter_TracksStepChangeWithoutJumpingImmediately(t *testing.T) {
	samples := []float64{800, 805, 795, 1200, 1210, 1190, 1205, 1195}

	filter := NewKalmanFilter(DefaultKalmanConfig, samples[0])
	estimates := make([]float64, 0, len(samples))
	for _, sample := range samples {
		estimates = append(estimates, filter.Update(sample))
	}

	assert.Greater(t, estimates[len(estimates)-1], 1100.0)
	assert.Less(t, estimates[3], 1100.0)
}

func TestNewKalmanFilter_InvalidConfigFallsBackToDefaults(t *testing.T) {
	filter := NewKalmanFilter(KalmanConfig{}, 900)
	estimate := filter.Update(1000)

	assert.Greater(t, estimate, 900.0)
	assert.Less(t, estimate, 1000.0)
}
