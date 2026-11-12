package fans

import (
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// curveStartingAt returns curve data of a fan that stands still below startPwm and spins
// with 1000 RPM from startPwm on.
func curveStartingAt(startPwm int) map[int]float64 {
	result := map[int]float64{}
	for pwm := MinPwmValue; pwm <= MaxPwmValue; pwm++ {
		if pwm >= startPwm {
			result[pwm] = 1000
		} else {
			result[pwm] = 0
		}
	}
	return result
}

func TestHwMonFan_AttachFanRpmCurveData_Twice_RecomputesStartPwm(t *testing.T) {
	// GIVEN: a fan without a configured startPwm, analyzed once
	fan := &HwMonFan{Config: configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{}}}
	first := curveStartingAt(50)
	require.NoError(t, fan.AttachFanRpmCurveData(&first))
	require.Equal(t, 50, fan.GetStartPwm())

	// WHEN: it is analyzed again and starts at a different PWM value now
	second := curveStartingAt(100)
	err := fan.AttachFanRpmCurveData(&second)

	// THEN: the start PWM is computed from the new data, not kept from the previous one
	assert.NoError(t, err)
	assert.Equal(t, 100, fan.GetStartPwm())
	startPwm, _ := ComputePwmBoundaries(fan)
	assert.Equal(t, 100, startPwm)
}

func TestHwMonFan_AttachFanRpmCurveData_Twice_KeepsConfiguredStartPwm(t *testing.T) {
	// GIVEN: a fan with a configured startPwm
	configured := 70
	fan := &HwMonFan{
		Config:   configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{}, StartPwm: &configured},
		StartPwm: &configured,
	}
	first := curveStartingAt(50)
	require.NoError(t, fan.AttachFanRpmCurveData(&first))

	// WHEN
	second := curveStartingAt(100)
	err := fan.AttachFanRpmCurveData(&second)

	// THEN: the configured value always wins
	assert.NoError(t, err)
	assert.Equal(t, configured, fan.GetStartPwm())
	startPwm, _ := ComputePwmBoundaries(fan)
	assert.Equal(t, configured, startPwm)
}

func TestComputePwmBoundaries_NoCurveData(t *testing.T) {
	// GIVEN: a fan without RPM sensor, which has no curve data
	fan := &HwMonFan{Config: configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{}}}
	require.NoError(t, fan.AttachFanRpmCurveData(nil))

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, MaxPwmValue, startPwm)
	assert.Equal(t, MaxPwmValue, maxPwm)
}

func TestComputePwmBoundaries_IgnoresComputedStartPwm(t *testing.T) {
	// GIVEN: a fan whose current start PWM was computed from older curve data
	computed := 30
	fan := &HwMonFan{
		Config:   configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{}},
		StartPwm: &computed,
	}
	data := curveStartingAt(100)
	fan.FanCurveData = &data

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 100, startPwm)
	assert.Equal(t, MaxPwmValue, maxPwm)
}
