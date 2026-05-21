package controller

import (
	"fmt"
	"math"
	"testing"

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

func TestDefaultFanController_AggregateRpmSamples_SyntheticCurveMeasurementScenarios(t *testing.T) {
	controller := DefaultFanController{}
	scenarios := syntheticAggregationScenarios()

	maxSamples := 0
	for _, s := range scenarios {
		if len(s.rpmSeries) > maxSamples {
			maxSamples = len(s.rpmSeries)
		}
	}

	for sampleCount := 2; sampleCount <= maxSamples; sampleCount++ {
		totalKalmanErr := 0.0
		totalMedianErr := 0.0
		cases := 0

		for _, scenario := range scenarios {
			if len(scenario.rpmSeries) < sampleCount {
				continue
			}
			samples := scenario.rpmSeries[:sampleCount]
			kalmanEstimate := controller.aggregateRpmSamples(samples)
			medianEstimate := util.MedianFloat64(samples)

			kalmanErr := math.Abs(kalmanEstimate - scenario.trueRPM)
			medianErr := math.Abs(medianEstimate - scenario.trueRPM)

			totalKalmanErr += kalmanErr
			totalMedianErr += medianErr
			cases++

			assert.False(t, math.IsNaN(kalmanEstimate), "scenario=%s sampleCount=%d", scenario.name, sampleCount)
			assert.False(t, math.IsInf(kalmanEstimate, 0), "scenario=%s sampleCount=%d", scenario.name, sampleCount)
		}

		if cases == 0 {
			continue
		}

		avgKalmanErr := totalKalmanErr / float64(cases)
		avgMedianErr := totalMedianErr / float64(cases)
		t.Logf("sampleCount=%d avgAbsErr kalman=%.2f median=%.2f delta=%.2f", sampleCount, avgKalmanErr, avgMedianErr, avgKalmanErr-avgMedianErr)
	}
}

func BenchmarkDefaultFanController_AggregateRpmSamplesVsMedian(b *testing.B) {
	controller := DefaultFanController{}
	scenarios := syntheticAggregationScenarios()

	for _, scenario := range scenarios {
		scenario := scenario
		b.Run(fmt.Sprintf("kalman_%s", scenario.name), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = controller.aggregateRpmSamples(scenario.rpmSeries)
			}
		})
		b.Run(fmt.Sprintf("median_%s", scenario.name), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = util.MedianFloat64(scenario.rpmSeries)
			}
		})
	}
}
