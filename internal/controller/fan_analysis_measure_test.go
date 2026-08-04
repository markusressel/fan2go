package controller

import (
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
)

// mockFanLaggingPwmReadback is a fan whose reported PWM converges to the requested value only after lag has passed.
type mockFanLaggingPwmReadback struct {
	MockFan
	lag          time.Duration
	targetPwm    int
	reportedPwm  int
	lastChangeAt time.Time
}

func (fan *mockFanLaggingPwmReadback) SetPwm(pwm int) error {
	if pwm != fan.targetPwm {
		fan.reportedPwm = fan.targetPwm
		fan.targetPwm = pwm
		fan.lastChangeAt = time.Now()
	}
	return nil
}

func (fan *mockFanLaggingPwmReadback) GetPwm() (int, error) {
	if time.Since(fan.lastChangeAt) >= fan.lag {
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
	configuration.CurrentConfig.FanResponseDelay = 1
	configuration.CurrentConfig.Analysis.SampleCount = 1
	configuration.CurrentConfig.Analysis.SampleDelay = 0

	fan := &mockFanLaggingPwmReadback{
		MockFan: MockFan{
			ID:  "fan",
			RPM: 900,
		},
		lag: 300 * time.Millisecond,
	}
	controller := &DefaultFanController{
		fan: fan,
	}
	for i := 0; i < 256; i++ {
		controller.pwmMapping[i] = i
	}
	analyzer := NewFanCurveAnalyzer(controller)

	// WHEN
	rpm, err := analyzer.measureAtPwm(fan, 128, 0)

	// THEN: after FanResponseDelay the reported PWM converges and the RPM is measured
	assert.NoError(t, err)
	assert.Equal(t, 900.0, rpm, "measureAtPwm should wait FanResponseDelay for the reported PWM to converge instead of skipping the sample")
}
