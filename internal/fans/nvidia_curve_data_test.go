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

func TestNvidiaFan_AttachFanRpmCurveData_ClampsDerivedMinPwm(t *testing.T) {
	// GIVEN: the measured start is above the configured maxPwm
	maxPwm := 100
	fan := &NvidiaFan{
		Config: configuration.FanConfig{ID: "gpu", Nvidia: &configuration.NvidiaFanConfig{}, MaxPwm: &maxPwm},
		MaxPwm: &maxPwm,
	}
	curveData := curveStartingAt(120)

	// WHEN
	err := fan.AttachFanRpmCurveData(&curveData)

	// THEN: the configured maxPwm wins
	assert.NoError(t, err)
	assert.Equal(t, 100, fan.GetMinPwm())
	assert.Equal(t, 100, fan.GetStartPwm())
	assert.Equal(t, 100, fan.GetMaxPwm())
}

func TestNvidiaFan_AttachFanRpmCurveData_MaxPwmCanOnlyBeRaisedTo100(t *testing.T) {
	// GIVEN: a configured startPwm above the measured maxPwm and above the nvidia limit of 100
	startPwm := 150
	fan := &NvidiaFan{
		Config:   configuration.FanConfig{ID: "gpu", Nvidia: &configuration.NvidiaFanConfig{}, StartPwm: &startPwm},
		StartPwm: &startPwm,
	}
	curveData := map[int]float64{0: 0, 30: 1000, 80: 2000}
	warnings := captureWarnings(t)

	// WHEN
	err := fan.AttachFanRpmCurveData(&curveData)

	// THEN: maxPwm is raised as far as possible, the derived minPwm is lowered to it, the configured startPwm is kept
	assert.NoError(t, err)
	assert.Equal(t, 100, fan.GetMaxPwm())
	assert.Equal(t, 100, fan.GetMinPwm())
	assert.Equal(t, 150, fan.GetStartPwm())
	assert.Contains(t, warnings.String(), "Fan gpu: the derived maxPwm is below minPwm (150) or startPwm (150), using maxPwm 100")
	assert.Contains(t, warnings.String(), "Fan gpu: the derived minPwm (150) is above maxPwm (100), using minPwm 100")
	assert.Contains(t, warnings.String(), "Fan gpu: the configured startPwm (150) is above the highest possible maxPwm (100)")
}

func TestNvidiaFan_AttachFanRpmCurveData_FlatCurve_MaxPwmIs100(t *testing.T) {
	// GIVEN: a fan that doesn't react to the fan speed setting (values are percent)
	fan := &NvidiaFan{Config: configuration.FanConfig{ID: "gpu", Nvidia: &configuration.NvidiaFanConfig{}}, CanReadRPM: true}
	curve := map[int]float64{0: 0}
	for pwm := 1; pwm <= 100; pwm++ {
		curve[pwm] = 1500
	}

	// WHEN
	require.NoError(t, fan.AttachFanRpmCurveData(&curve))

	// THEN: the full range of the fan
	assert.Equal(t, 1, fan.GetStartPwm())
	assert.Equal(t, 100, fan.GetMaxPwm())
}
