package fans

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// it87Emulator emulates the pwm and pwm_enable files of one channel of the it87 driver: pwm_mode() reports 0 for a
// channel in manual mode at duty 255 (chips without FEAT_FANCTL_ONOFF), and set_pwm() returns EBUSY in automatic
// mode. With onOffMode, the channel stays in on/off mode ("full speed"): pwm_enable reads 0 whatever is written.
type it87Emulator struct {
	t             *testing.T
	pwmPath       string
	pwmEnablePath string
	automatic     bool
	duty          int
	onOffMode     bool
}

// newIt87Emulator creates the files of pwm2 in a temp dir, in automatic mode at the given duty, and replaces
// writeSysfsInt with the emulator until the end of the test.
func newIt87Emulator(t *testing.T, duty int) *it87Emulator {
	dir := t.TempDir()
	e := &it87Emulator{
		t:             t,
		pwmPath:       filepath.Join(dir, "pwm2"),
		pwmEnablePath: filepath.Join(dir, "pwm2_enable"),
		automatic:     true,
		duty:          duty,
	}
	e.sync()
	previous := writeSysfsInt
	writeSysfsInt = e.write
	t.Cleanup(func() { writeSysfsInt = previous })
	return e
}

func (e *it87Emulator) pwmEnable() int {
	switch {
	case e.onOffMode:
		return 0
	case e.automatic:
		return 2
	case e.duty == MaxPwmValue:
		return 0
	default:
		return 1
	}
}

func (e *it87Emulator) sync() {
	require.NoError(e.t, os.WriteFile(e.pwmPath, []byte(strconv.Itoa(e.duty)), 0o644))
	require.NoError(e.t, os.WriteFile(e.pwmEnablePath, []byte(strconv.Itoa(e.pwmEnable())), 0o644))
}

func (e *it87Emulator) write(value int, path string) error {
	switch path {
	case e.pwmPath:
		if e.automatic {
			return &os.PathError{Op: "write", Path: path, Err: syscall.EBUSY}
		}
		e.duty = value
	case e.pwmEnablePath:
		switch value {
		case 0:
			e.automatic = false
			e.duty = MaxPwmValue
		case 1:
			e.automatic = false
		case 2:
			e.automatic = true
		default:
			return &os.PathError{Op: "write", Path: path, Err: syscall.EINVAL}
		}
	default:
		return errors.New("unexpected path " + path)
	}
	e.sync()
	return nil
}

func (e *it87Emulator) fan(t *testing.T, hwmonName string, quirksEnabled bool) *HwMonFan {
	config := configuration.FanConfig{
		ID: "front_chassis",
		HwMon: &configuration.HwMonFanConfig{
			Platform: "it8665-isa-0290", RpmChannel: 2, PwmChannel: 2,
			PwmPath: e.pwmPath, PwmEnablePath: e.pwmEnablePath,
			HwmonName: hwmonName, Modalias: "platform:it87",
		},
	}
	if !quirksEnabled {
		config.Quirks = &configuration.QuirksConfig{}
		config.Quirks.Enabled.SetOverride(false)
	}
	fan, err := NewFan(config)
	require.NoError(t, err)
	return fan.(*HwMonFan)
}

func TestHwMonFan_FullDutyReadsAsFullSpeed_SetControlModeAtPwm255(t *testing.T) {
	// GIVEN: the board's automatic curve drove the fan to 255
	emulator := newIt87Emulator(t, 255)
	fan := emulator.fan(t, "it8665", true)

	// WHEN
	err := fan.SetControlMode(ControlModePWM)

	// THEN: pwm_enable reads 0 at pwm 255, which is manual mode on this chip
	require.NoError(t, err)
	assert.False(t, emulator.automatic)
	assert.Equal(t, 0, readInt(t, emulator.pwmEnablePath))
	mode, err := fan.GetControlMode()
	require.NoError(t, err)
	assert.Equal(t, ControlModePWM, mode)

	// WHEN: fan2go writes 255 (no confirmation possible), then a lower value
	require.NoError(t, fan.SetPwm(255))
	err = fan.SetPwm(128)

	// THEN: pwm_enable reads 1, manual mode is confirmed
	require.NoError(t, err)
	assert.Equal(t, 1, readInt(t, emulator.pwmEnablePath))

	// WHEN: fan2go's curve asks for 255 again, and the sanity check reads the mode
	require.NoError(t, fan.SetPwm(255))
	mode, err = fan.GetControlMode()

	// THEN: still manual mode, no workaround value is written
	require.NoError(t, err)
	assert.Equal(t, ControlModePWM, mode)
	assert.Equal(t, 255, readInt(t, emulator.pwmPath))
}

func TestHwMonFan_FullDutyReadsAsFullSpeed_PwmEnableStaysZeroAtLowerPwm_Error(t *testing.T) {
	// GIVEN: a channel that is in on/off mode, i.e. it isn't controllable with pwm
	emulator := newIt87Emulator(t, 255)
	emulator.onOffMode = true
	fan := emulator.fan(t, "it8665", true)
	require.NoError(t, fan.SetControlMode(ControlModePWM))

	// WHEN
	err := fan.SetPwm(128)

	// THEN
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PWM mode stuck to 0")
	assert.Contains(t, err.Error(), "it87")

	// WHEN: the sanity check reads the mode
	mode, err := fan.GetControlMode()

	// THEN: pwm is not 255 anymore, so 0 means disabled again
	require.NoError(t, err)
	assert.Equal(t, ControlModeDisabled, mode)
	// and switching to manual mode fails like on other drivers
	assert.EqualError(t, fan.SetControlMode(ControlModePWM), "PWM mode stuck to 0")
}

func TestHwMonFan_FullDutyReadsAsFullSpeed_ConfirmedOnlyOnce(t *testing.T) {
	// GIVEN
	emulator := newIt87Emulator(t, 255)
	fan := emulator.fan(t, "it8665", true)
	require.NoError(t, fan.SetControlMode(ControlModePWM))
	require.NoError(t, fan.SetPwm(100))

	// WHEN: something switches the channel to on/off mode later
	emulator.onOffMode = true
	emulator.sync()
	err := fan.SetPwm(90)

	// THEN: SetPwm doesn't check again, the mode sanity check reports it (pwm is not 255)
	require.NoError(t, err)
	mode, err := fan.GetControlMode()
	require.NoError(t, err)
	assert.Equal(t, ControlModeDisabled, mode)
}

func TestHwMonFan_FullDutyReadsAsFullSpeed_UnmatchedChip_StuckError(t *testing.T) {
	tests := []struct {
		name          string
		hwmonName     string
		quirksEnabled bool
	}{
		// FEAT_FANCTL_ONOFF and only 3 pwm channels: pwm_enable 0 means on/off mode
		{"chip with fan on/off control", "it8728", true},
		// another driver: pwm_enable 0 can mean full speed without pwm control
		{"other driver", "nct6798", true},
		{"quirks disabled", "it8665", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			emulator := newIt87Emulator(t, 255)
			fan := emulator.fan(t, tt.hwmonName, tt.quirksEnabled)

			// WHEN
			err := fan.SetControlMode(ControlModePWM)

			// THEN: the old behaviour
			assert.EqualError(t, err, "PWM mode stuck to 0")
			mode, err := fan.GetControlMode()
			require.NoError(t, err)
			assert.Equal(t, ControlModeDisabled, mode)
		})
	}
}

func TestHwMonFan_FullDutyReadsAsFullSpeed_ChannelBelowFirstChannel(t *testing.T) {
	// GIVEN: it8628 has fan on/off control on pwm1-3, only pwm4 and above report 0 at pwm 255
	emulator := newIt87Emulator(t, 255)
	fan := emulator.fan(t, "it8628", true)
	require.NotNil(t, KnownQuirksOf(fan))

	// WHEN: pwm2
	err := fan.SetControlMode(ControlModePWM)

	// THEN
	assert.EqualError(t, err, "PWM mode stuck to 0")

	// WHEN: pwm4
	fan.Config.HwMon.PwmChannel = 4
	err = fan.SetControlMode(ControlModePWM)

	// THEN
	assert.NoError(t, err)
}

func TestHwMonFan_FullDutyReadsAsFullSpeed_PwmNot255_Disabled(t *testing.T) {
	// GIVEN: pwm_enable 0, but pwm is not 255 (e.g. on/off mode with a stored duty)
	emulator := newIt87Emulator(t, 128)
	emulator.automatic = false
	emulator.onOffMode = true
	emulator.sync()
	fan := emulator.fan(t, "it8665", true)

	// WHEN
	mode, err := fan.GetControlMode()

	// THEN
	require.NoError(t, err)
	assert.Equal(t, ControlModeDisabled, mode)
}

func TestHwMonFan_FullDutyReadsAsFullSpeed_ManualModeFirst_NoWriteInAutomaticMode(t *testing.T) {
	// GIVEN: the driver rejects pwm writes in automatic mode (EBUSY)
	emulator := newIt87Emulator(t, 200)
	fan := emulator.fan(t, "it8665", true)
	require.Error(t, fan.SetPwm(128))

	// WHEN: manual mode first, then pwm
	require.NoError(t, fan.SetControlMode(ControlModePWM))
	err := fan.SetPwm(128)

	// THEN
	require.NoError(t, err)
	assert.Equal(t, 1, readInt(t, emulator.pwmEnablePath))
}

func readInt(t *testing.T, path string) int {
	t.Helper()
	value, err := util.ReadIntFromFile(path)
	require.NoError(t, err)
	return value
}
