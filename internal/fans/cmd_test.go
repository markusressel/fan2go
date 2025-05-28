package fans

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"os/exec"
	"testing"
)

func getEchoPath() string {
	// unlikely to fail
	p, _ := exec.LookPath("echo")
	return p
}

func TestCmdFan_NewFan(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}

	// WHEN
	fan, err := NewFan(config)

	// THEN
	assert.NoError(t, err)
	assert.NotNil(t, fan)
}

func TestCmdFan_GetId(t *testing.T) {
	// GIVEN
	id := "test"
	config := configuration.FanConfig{
		ID:  id,
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.GetId()

	// THEN
	assert.Equal(t, id, result)
}

func TestCmdFan_GetStartPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.GetStartPwm()

	// THEN
	assert.Equal(t, 1, result)
}

func TestCmdFan_SetStartPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	fan.SetStartPwm(1, false)

	// THEN
	result := fan.GetStartPwm()
	// Note: CmdFan does not support setting the start PWM value
	assert.Equal(t, 1, result)
}

func TestCmdFan_GetMinPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.GetMinPwm()

	// THEN
	assert.Equal(t, MinPwmValue, result)
}

func TestCmdFan_SetMinPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	fan.SetMinPwm(0, false)

	// THEN
	result := fan.GetMinPwm()
	// Note: CmdFan does not support setting the min PWM value
	assert.Equal(t, MinPwmValue, result)
}

func TestCmdFan_GetMaxPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.GetMaxPwm()

	// THEN
	assert.Equal(t, MaxPwmValue, result)
}

func TestCmdFan_SetMaxPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	fan.SetMaxPwm(100, false)

	// THEN
	result := fan.GetMaxPwm()
	// Note: CmdFan does not support setting the max PWM value
	assert.Equal(t, MaxPwmValue, result)
}

func TestCmdFan_GetRpm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{
			GetRpm: &configuration.ExecConfig{
				Exec: getEchoPath(),
				Args: []string{"1000"},
			},
		},
	}
	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetRpm()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 1000, result)
}

func TestCmdFan_GetRpm_CommandError(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{
			GetRpm: &configuration.ExecConfig{
				Exec: "/usr/bin/does_not_exist",
			},
		},
	}
	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetRpm()

	// THEN
	assert.Error(t, err)
	assert.Equal(t, 0, result)
}

func TestCmdFan_GetRpm_ParseError(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{
			GetRpm: &configuration.ExecConfig{
				Exec: getEchoPath(),
				Args: []string{"not_a_number"},
			},
		},
	}
	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetRpm()

	// THEN
	assert.Error(t, err)
	assert.Equal(t, 0, result)
}

func TestCmdFan_GetRpm_NoSupport(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetRpm()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 0, result)
}

func TestCmdFan_GetRpm_Timeout(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{
			GetRpm: &configuration.ExecConfig{
				Exec: "/usr/bin/sleep",
				Args: []string{"5"},
			},
		},
	}
	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetRpm()

	// THEN
	assert.Error(t, err)
	assert.Equal(t, 0, result)
}

func TestCmdFan_GetRpmAvg(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{
			GetRpm: &configuration.ExecConfig{
				Exec: getEchoPath(),
				Args: []string{"1000"},
			},
		},
	}
	fan, _ := NewFan(config)
	_, err := fan.GetRpm()
	assert.NoError(t, err)

	// WHEN
	result := fan.GetRpmAvg()

	// THEN
	assert.Equal(t, 1000.0, result)
}

func TestCmdFan_SetRpmAvg(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	fan.SetRpmAvg(1000)

	// THEN
	result := fan.GetRpmAvg()
	assert.Equal(t, 1000.0, result)
}

func TestCmdFan_GetPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{
			GetPwm: &configuration.ExecConfig{
				Exec: getEchoPath(),
				Args: []string{"255"},
			},
		},
	}
	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetPwm()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 255, result)
}

func TestCmdFan_SetPwm(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{
			SetPwm: &configuration.ExecConfig{
				Exec: getEchoPath(),
				Args: []string{"%pwm%"},
			},
		},
	}
	fan, _ := NewFan(config)

	// WHEN
	err := fan.SetPwm(255)

	// THEN
	assert.NoError(t, err)
}

func TestCmdFan_SetPwm_Error(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{
			SetPwm: &configuration.ExecConfig{
				Exec: "/usr/bin/does_not_exist",
				Args: []string{"%pwm%"},
			},
		},
	}
	fan, _ := NewFan(config)

	// WHEN
	err := fan.SetPwm(255)

	// THEN
	assert.Error(t, err)
}

func TestCmdFan_SetPwm_Timeout(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{
			SetPwm: &configuration.ExecConfig{
				Exec: "/usr/bin/sleep",
				Args: []string{"5"},
			},
		},
	}
	fan, _ := NewFan(config)

	// WHEN
	err := fan.SetPwm(255)

	// THEN
	assert.Error(t, err)
}

func TestCmdFan_GetFanCurveData(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	var interpolated, err = util.InterpolateLinearly(&map[int]float64{0: 0, 255: 255}, 0, 255)
	assert.NoError(t, err)

	// WHEN
	result := fan.GetFanRpmCurveData()

	// THEN
	assert.Equal(t, &interpolated, result)
}

func TestCmdFan_AttachFanCurveData(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	err := fan.AttachFanRpmCurveData(nil)

	// THEN
	assert.NoError(t, err)
}

func TestCmdFan_GetCurveId(t *testing.T) {
	// GIVEN
	curveId := "curveId"
	config := configuration.FanConfig{
		Curve: curveId,
		Cmd:   &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.GetCurveId()

	// THEN
	assert.Equal(t, curveId, result)
}

func TestCmdFan_ShouldNeverStop(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		NeverStop: true,
		Cmd:       &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.ShouldNeverStop()

	// THEN
	assert.True(t, result)
}

func TestCmdFan_GetPwmEnabled(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.GetPwmEnabled()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 1, result)
}

func TestCmdFan_IsPwmAuto(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result, err := fan.IsPwmAuto()

	// THEN
	assert.NoError(t, err)
	assert.True(t, result)
}

func TestCmdFan_SetPwmEnabled(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	err := fan.SetPwmEnabled(ControlModeAutomatic)

	// THEN
	assert.NoError(t, err)
	result, err := fan.GetPwmEnabled()
	assert.NoError(t, err)
	// Note: CmdFan does not support setting the PWM enabled value
	assert.Equal(t, 1, result)
}

func TestCmdFan_Supports_ControlMode(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.Supports(FeatureControlMode)

	// THEN
	assert.False(t, result)
}

func TestCmdFan_Supports_RpmSensor_False(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.Supports(FeatureRpmSensor)

	// THEN
	assert.False(t, result)
}

func TestCmdFan_Supports_RpmSensor_True(t *testing.T) {
	// GIVEN
	config := configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{
			GetRpm: &configuration.ExecConfig{
				Exec: getEchoPath(),
				Args: []string{"1000"},
			},
		},
	}
	fan, _ := NewFan(config)

	// WHEN
	result := fan.Supports(FeatureRpmSensor)

	// THEN
	assert.True(t, result)
}
