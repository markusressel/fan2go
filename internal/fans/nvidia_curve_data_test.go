//go:build !disable_nvml

package fans

import (
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNvidiaFan_AttachFanRpmCurveData_Empty(t *testing.T) {
	for name, curveData := range map[string]*map[int]float64{
		"nil pointer": nil,
		"nil map":     new(map[int]float64),
		"empty map":   {},
	} {
		t.Run(name, func(t *testing.T) {
			// GIVEN: a fan without RPM sensor, which can't be measured
			fan := &NvidiaFan{Config: configuration.FanConfig{ID: "gpu", Nvidia: &configuration.NvidiaFanConfig{}}}
			minPwm, startPwm, maxPwm := fan.GetMinPwm(), fan.GetStartPwm(), fan.GetMaxPwm()

			// WHEN
			err := fan.AttachFanRpmCurveData(curveData)

			// THEN: like HwMonFan, this means "no curve data", not an error,
			// and the default PWM boundaries are kept
			assert.NoError(t, err)
			assert.Nil(t, fan.GetFanRpmCurveData())
			assert.Equal(t, minPwm, fan.GetMinPwm())
			assert.Equal(t, startPwm, fan.GetStartPwm())
			assert.Equal(t, maxPwm, fan.GetMaxPwm())
		})
	}
}

func TestNvidiaFan_AttachFanRpmCurveData_Twice_RecomputesStartPwm(t *testing.T) {
	// GIVEN: a fan without a configured startPwm, analyzed once
	fan := &NvidiaFan{Config: configuration.FanConfig{ID: "gpu", Nvidia: &configuration.NvidiaFanConfig{}}}
	first := curveStartingAt(50)
	require.NoError(t, fan.AttachFanRpmCurveData(&first))
	require.Equal(t, 50, fan.GetStartPwm())

	// WHEN: it is analyzed again and starts at a different PWM value now
	second := curveStartingAt(100)
	err := fan.AttachFanRpmCurveData(&second)

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 100, fan.GetStartPwm())
}
