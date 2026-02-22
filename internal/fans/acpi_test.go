package fans

import (
	"fmt"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAcpiFan(acpiConfig *configuration.AcpiFanConfig) *AcpiFan {
	return &AcpiFan{
		Config: configuration.FanConfig{
			ID:   "acpi_test",
			Acpi: acpiConfig,
		},
	}
}

func TestAcpiFan_NewFan(t *testing.T) {
	config := configuration.FanConfig{
		Acpi: &configuration.AcpiFanConfig{
			SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
		},
	}
	fan, err := NewFan(config)
	assert.NoError(t, err)
	assert.NotNil(t, fan)
}

func TestAcpiFan_GetId(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	assert.Equal(t, "acpi_test", fan.GetId())
}

func TestAcpiFan_GetLabel(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	assert.Equal(t, "ACPI Fan acpi_test", fan.GetLabel())
}

func TestAcpiFan_GetMinPwm(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	assert.Equal(t, MinPwmValue, fan.GetMinPwm())
}

func TestAcpiFan_GetMaxPwm(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	assert.Equal(t, MaxPwmValue, fan.GetMaxPwm())
}

func TestAcpiFan_GetStartPwm(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	assert.Equal(t, 1, fan.GetStartPwm())
}

func TestAcpiFan_SetPwm_PwmConversion(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{
			Method:     `\_SB.METH`,
			Args:       "%pwm%",
			Conversion: configuration.AcpiFanConversionPwm,
		},
	})

	var gotMethod, gotArgs string
	mockCall := func(method, args string) (int64, error) {
		gotMethod = method
		gotArgs = args
		return 0, nil
	}

	err := fan.setPwmAt(mockCall, 128)
	require.NoError(t, err)
	assert.Equal(t, `\_SB.METH`, gotMethod)
	assert.Equal(t, "128", gotArgs)
	assert.Equal(t, 128, fan.Pwm)
}

func TestAcpiFan_SetPwm_PercentageConversion(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{
			Method:     `\_SB.METH`,
			Args:       "%pwm%",
			Conversion: configuration.AcpiFanConversionPercentage,
		},
	})

	var gotArgs string
	mockCall := func(method, args string) (int64, error) {
		gotArgs = args
		return 0, nil
	}

	// 255 PWM → 100%
	err := fan.setPwmAt(mockCall, 255)
	require.NoError(t, err)
	assert.Equal(t, "100", gotArgs)

	// 0 PWM → 0%
	err = fan.setPwmAt(mockCall, 0)
	require.NoError(t, err)
	assert.Equal(t, "0", gotArgs)

	// ~128 PWM → ~50%
	err = fan.setPwmAt(mockCall, 128)
	require.NoError(t, err)
	assert.Equal(t, "50", gotArgs)
}

func TestAcpiFan_SetPwm_DefaultConversion(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{
			Method: `\_SB.METH`,
			Args:   "%pwm%",
			// no conversion set → default pwm pass-through
		},
	})

	var gotArgs string
	mockCall := func(method, args string) (int64, error) {
		gotArgs = args
		return 0, nil
	}

	err := fan.setPwmAt(mockCall, 200)
	require.NoError(t, err)
	assert.Equal(t, "200", gotArgs)
}

func TestAcpiFan_SetPwm_Error(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{
			Method: `\_SB.METH`,
			Args:   "%pwm%",
		},
	})

	mockCall := func(method, args string) (int64, error) {
		return 0, fmt.Errorf("acpi_call: write failed")
	}

	err := fan.setPwmAt(mockCall, 128)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acpi_test")
}

func TestAcpiFan_GetPwm_PwmConversion(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
		GetPwm: &configuration.AcpiFanCallConfig{
			Method:     `\_SB.METH`,
			Conversion: configuration.AcpiFanConversionPwm,
		},
	})

	mockCall := func(method, args string) (int64, error) {
		return 200, nil
	}

	pwm, err := fan.getPwmAt(mockCall)
	require.NoError(t, err)
	assert.Equal(t, 200, pwm)
}

func TestAcpiFan_GetPwm_PercentageConversion(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
		GetPwm: &configuration.AcpiFanCallConfig{
			Method:     `\_SB.METH`,
			Conversion: configuration.AcpiFanConversionPercentage,
		},
	})

	mockCall := func(method, args string) (int64, error) {
		return 100, nil // 100% → 255 PWM
	}

	pwm, err := fan.getPwmAt(mockCall)
	require.NoError(t, err)
	assert.Equal(t, 255, pwm)
}

func TestAcpiFan_GetRpm(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
		GetRpm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})

	mockCall := func(method, args string) (int64, error) {
		return 1500, nil
	}

	rpm, err := fan.getRpmAt(mockCall)
	require.NoError(t, err)
	assert.Equal(t, 1500, rpm)
	assert.Equal(t, 1500, fan.Rpm)
}

func TestAcpiFan_GetRpm_NoSupport(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
		// no GetRpm configured
	})

	rpm, err := fan.GetRpm()
	require.NoError(t, err)
	assert.Equal(t, 0, rpm)
}

func TestAcpiFan_GetRpmAvg(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	fan.SetRpmAvg(1200)
	assert.Equal(t, 1200.0, fan.GetRpmAvg())
}

func TestAcpiFan_GetFanRpmCurveData(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})

	expected, err := util.InterpolateLinearly(&map[int]float64{0: 0, 255: 255}, 0, 255)
	require.NoError(t, err)

	result := fan.GetFanRpmCurveData()
	assert.Equal(t, &expected, result)
}

func TestAcpiFan_GetCurveId(t *testing.T) {
	fan := &AcpiFan{
		Config: configuration.FanConfig{
			ID:    "acpi_test",
			Curve: "my_curve",
			Acpi:  &configuration.AcpiFanConfig{SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`}},
		},
	}
	assert.Equal(t, "my_curve", fan.GetCurveId())
}

func TestAcpiFan_ShouldNeverStop(t *testing.T) {
	fan := &AcpiFan{
		Config: configuration.FanConfig{
			ID:        "acpi_test",
			NeverStop: true,
			Acpi:      &configuration.AcpiFanConfig{SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`}},
		},
	}
	assert.True(t, fan.ShouldNeverStop())
}

func TestAcpiFan_Supports_ControlModeWrite(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	assert.False(t, fan.Supports(FeatureControlModeWrite))
}

func TestAcpiFan_Supports_ControlModeRead(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	assert.False(t, fan.Supports(FeatureControlModeRead))
}

func TestAcpiFan_Supports_PwmSensor_False(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
		// no GetPwm
	})
	assert.False(t, fan.Supports(FeaturePwmSensor))
}

func TestAcpiFan_Supports_PwmSensor_True(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
		GetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	assert.True(t, fan.Supports(FeaturePwmSensor))
}

func TestAcpiFan_Supports_RpmSensor_False(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
		// no GetRpm
	})
	assert.False(t, fan.Supports(FeatureRpmSensor))
}

func TestAcpiFan_Supports_RpmSensor_True(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
		GetRpm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	assert.True(t, fan.Supports(FeatureRpmSensor))
}

func TestAcpiFan_GetControlMode(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	mode, err := fan.GetControlMode()
	require.NoError(t, err)
	assert.Equal(t, ControlModePWM, mode)
}

func TestAcpiFan_SetControlMode(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	err := fan.SetControlMode(ControlModeAutomatic)
	require.NoError(t, err)
	// Control mode setting is not supported, stays at PWM
	mode, err := fan.GetControlMode()
	require.NoError(t, err)
	assert.Equal(t, ControlModePWM, mode)
}

func TestAcpiFan_AttachFanRpmCurveData(t *testing.T) {
	fan := newAcpiFan(&configuration.AcpiFanConfig{
		SetPwm: &configuration.AcpiFanCallConfig{Method: `\_SB.METH`},
	})
	err := fan.AttachFanRpmCurveData(nil)
	assert.NoError(t, err)
}
