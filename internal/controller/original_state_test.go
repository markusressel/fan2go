package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingModeFan is a panickingFan whose control mode writes fail while failModeWrites is set.
type failingModeFan struct {
	*panickingFan
	failMu         sync.Mutex
	failModeWrites bool
}

func (fan *failingModeFan) setFailModeWrites(fail bool) {
	fan.failMu.Lock()
	defer fan.failMu.Unlock()
	fan.failModeWrites = fail
}

func (fan *failingModeFan) SetControlMode(mode fans.ControlMode) error {
	fan.failMu.Lock()
	fail := fan.failModeWrites
	fan.failMu.Unlock()
	if fail {
		return errors.New("control mode write failed")
	}
	return fan.panickingFan.SetControlMode(mode)
}

// startOriginalStateRun starts Run for fan (see sensorFailureTest) with the persistence p, and waits until
// the update loop set the fan to the curve value (50).
func startOriginalStateRun(t *testing.T, fan fans.Fan, p persistence.Persistence, onSensorFailure configuration.OnSensorFailureAction) *sensorFailureTest {
	t.Helper()
	test := startOriginalStateRunWithoutStartup(t, fan, p, onSensorFailure)
	advanceThroughRunStartup(t, test.clock)
	require.Eventually(t, func() bool {
		test.clock.Advance(time.Millisecond)
		pwm, _ := fan.GetPwm()
		return pwm == 50
	}, 5*time.Second, time.Millisecond)
	return test
}

func startOriginalStateRunWithoutStartup(t *testing.T, fan fans.Fan, p persistence.Persistence, onSensorFailure configuration.OnSensorFailureAction) *sensorFailureTest {
	t.Helper()
	t.Cleanup(setUpRunTestConfig(time.Hour))
	test := &sensorFailureTest{t: t}
	if pf, ok := fan.(*panickingFan); ok {
		test.fan = pf
	} else if ff, ok := fan.(*failingModeFan); ok {
		test.fan = ff.panickingFan
	}
	test.fan.onSensorFailure = onSensorFailure
	test.curve = &switchableCurve{value: 50}
	test.clock = util.NewFakeClock(time.Unix(1000, 0))
	test.controller = newRunTestController(fan, test.curve, time.Millisecond, test.clock)
	test.controller.persistence = p
	test.controller.errorNotifier = func(title, format string, a ...any) {
		test.notifiedMu.Lock()
		defer test.notifiedMu.Unlock()
		test.notified = append(test.notified, fmt.Sprintf(format, a...))
	}
	ctx, cancel := context.WithCancel(context.Background())
	test.cancel = cancel
	t.Cleanup(cancel)
	test.done = startRun(test.controller, ctx)
	return test
}

func intPointer(v int) *int { return &v }

func TestFanController_Run_SavesOriginalStateWhenTakingControl_DeletesItOnExit(t *testing.T) {
	// GIVEN: a fan in automatic mode at PWM 100
	fan := newPanickingFan()
	p := newFakePersistence()

	// WHEN: fan2go controls the fan
	test := startOriginalStateRun(t, fan, p, "")

	// THEN: its original state is saved
	saved, ok := p.originalState("fan")
	require.True(t, ok)
	assert.Equal(t, persistence.FanOriginalState{
		Hardware:    originalStateHardwareKey(fan.GetConfig()),
		ControlMode: intPointer(int(fans.ControlModeAutomatic)),
		Pwm:         intPointer(100),
	}, saved)

	// WHEN: fan2go stops
	test.stop()

	// THEN: the fan is handed back, and the saved state deleted
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, fan.getControlModeWrites())
	_, ok = p.originalState("fan")
	assert.False(t, ok)
	assert.Equal(t, 1, p.originalStateSaves)
}

func TestFanController_Run_CancelledBeforeTakingControl_SavesNothing(t *testing.T) {
	// GIVEN
	fan := newPanickingFan()
	p := newFakePersistence()
	test := startOriginalStateRunWithoutStartup(t, fan, p, "")
	test.clock.BlockUntil(1)

	// WHEN: fan2go stops during the warm-up
	test.cancel()
	require.Error(t, awaitRun(t, test.done))

	// THEN: the fan was never touched, nothing was saved
	assert.Empty(t, fan.getWrites())
	assert.Equal(t, 0, p.originalStateSaves)
	assert.Equal(t, 0, p.originalStateDeletes)
}

// After a kill, the fan is still in manual mode at the last PWM fan2go set. The next run restores the state
// the fan had before the killed run took control of it.
func TestFanController_Run_SavedOriginalStateWinsOverCurrentState(t *testing.T) {
	// GIVEN: a fan left in manual mode at PWM 80, and its saved original state (automatic)
	fan := newPanickingFan()
	fan.ControlMode = fans.ControlModePWM
	fan.PWM = 80
	p := newFakePersistence()
	p.originalStates["fan"] = persistence.FanOriginalState{
		Hardware:    originalStateHardwareKey(fan.GetConfig()),
		ControlMode: intPointer(int(fans.ControlModeAutomatic)),
		Pwm:         intPointer(120),
	}

	// WHEN: fan2go runs and stops
	test := startOriginalStateRun(t, fan, p, "")
	test.stop()

	// THEN: the fan is handed back to automatic control, and the saved state is deleted
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, fan.getControlModeWrites())
	assert.Equal(t, fans.ControlModeAutomatic, fan.ControlMode)
	_, ok := p.originalState("fan")
	assert.False(t, ok)
	assert.Equal(t, 0, p.originalStateSaves, "the saved state is not overwritten")
}

func TestFanController_Run_SavedOriginalState_RestoredAlsoWhenStoppedBeforeTakingControl(t *testing.T) {
	// GIVEN: a fan left in manual mode by a killed run, and its saved original state (automatic)
	fan := newPanickingFan()
	fan.ControlMode = fans.ControlModePWM
	fan.PWM = 80
	p := newFakePersistence()
	p.originalStates["fan"] = persistence.FanOriginalState{
		Hardware:    originalStateHardwareKey(fan.GetConfig()),
		ControlMode: intPointer(int(fans.ControlModeAutomatic)),
	}
	test := startOriginalStateRunWithoutStartup(t, fan, p, "")
	test.clock.BlockUntil(1)

	// WHEN: fan2go stops during the warm-up
	test.cancel()
	require.Error(t, awaitRun(t, test.done))

	// THEN: the fan is handed back anyway, it is not in its original state
	assert.Equal(t, []fans.ControlMode{fans.ControlModeAutomatic}, fan.getControlModeWrites())
	_, ok := p.originalState("fan")
	assert.False(t, ok)
}

func TestFanController_Run_SavedOriginalStateOfOtherHardware_IsIgnoredAndDeleted(t *testing.T) {
	// GIVEN: a saved state of another device with the same fan id
	fan := newPanickingFan()
	fan.ControlMode = fans.ControlModePWM
	fan.PWM = 80
	p := newFakePersistence()
	p.originalStates["fan"] = persistence.FanOriginalState{
		Hardware:    "hwmon:other/0/pwm1",
		ControlMode: intPointer(int(fans.ControlModeAutomatic)),
	}

	// WHEN
	test := startOriginalStateRun(t, fan, p, "")

	// THEN: the current state is the original one, and saved for this hardware
	saved, ok := p.originalState("fan")
	require.True(t, ok)
	assert.Equal(t, originalStateHardwareKey(fan.GetConfig()), saved.Hardware)
	assert.Equal(t, intPointer(int(fans.ControlModePWM)), saved.ControlMode)
	assert.Equal(t, intPointer(80), saved.Pwm)

	test.stop()
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModePWM}, fan.getControlModeWrites())
	assert.Equal(t, 80, fan.PWM)
	_, ok = p.originalState("fan")
	assert.False(t, ok)
}

// An incomplete restore keeps the saved state, so the next controller (after a reload or restart) restores the
// original state, not the state the incomplete restore left behind.
func TestFanController_Run_IncompleteRestore_KeepsSavedStateForTheNextController(t *testing.T) {
	// GIVEN: a fan in automatic mode, whose control mode can't be written when fan2go stops
	fan := &failingModeFan{panickingFan: newPanickingFan()}
	p := newFakePersistence()
	test := startOriginalStateRun(t, fan, p, "")
	fan.setFailModeWrites(true)

	// WHEN
	test.stop()

	// THEN: the fan is set to full speed instead, and the saved state is kept
	assert.Equal(t, 255, fan.PWM)
	assert.Equal(t, fans.ControlModePWM, fan.ControlMode)
	saved, ok := p.originalState("fan")
	require.True(t, ok)
	assert.Equal(t, intPointer(int(fans.ControlModeAutomatic)), saved.ControlMode)

	// WHEN: a new controller (e.g. after a reload) controls the fan, and stops
	fan.setFailModeWrites(false)
	test = startOriginalStateRun(t, fan, p, "")
	test.stop()

	// THEN: the fan is restored to the original (automatic) mode, not to manual mode at full speed
	assert.Equal(t, fans.ControlModeAutomatic, fan.ControlMode)
	_, ok = p.originalState("fan")
	assert.False(t, ok)
}

func TestFanController_Run_DatabaseErrors_FanIsControlledAndRestoredAsBefore(t *testing.T) {
	dbErr := errors.New("timeout")
	for name, setUp := range map[string]func(p *fakePersistence){
		"load fails":   func(p *fakePersistence) { p.loadOriginalStateErrors = []error{dbErr} },
		"save fails":   func(p *fakePersistence) { p.saveOriginalStateErrors = []error{dbErr} },
		"delete fails": func(p *fakePersistence) { p.deleteOriginalStateErrors = []error{dbErr} },
	} {
		t.Run(name, func(t *testing.T) {
			// GIVEN
			fan := newPanickingFan()
			p := newFakePersistence()
			setUp(p)

			// WHEN
			test := startOriginalStateRun(t, fan, p, "")
			test.stop()

			// THEN
			assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, fan.getControlModeWrites())
		})
	}

	t.Run("load fails: an existing saved state is neither overwritten nor deleted", func(t *testing.T) {
		fan := newPanickingFan()
		p := newFakePersistence()
		p.loadOriginalStateErrors = []error{dbErr}
		p.originalStates["fan"] = persistence.FanOriginalState{Hardware: originalStateHardwareKey(fan.GetConfig())}

		test := startOriginalStateRun(t, fan, p, "")
		test.stop()

		assert.Equal(t, 0, p.originalStateSaves)
		assert.Equal(t, 0, p.originalStateDeletes)
	})
}

func TestFanController_SensorFailure_Restore_DeletesSavedStateWhileHandedBack(t *testing.T) {
	// GIVEN
	fan := newPanickingFan()
	p := newFakePersistence()
	test := startOriginalStateRun(t, fan, p, configuration.OnSensorFailureRestore)

	// WHEN: the curve fails, the fan is handed back
	test.curve.set(0, errStale)
	test.awaitNotified(1)

	// THEN: fan2go doesn't control the fan, there is nothing to restore after a kill
	_, ok := p.originalState("fan")
	assert.False(t, ok)

	// WHEN: the curve recovers, fan2go takes control again
	test.curve.set(80, nil)
	test.awaitPwm(80)

	// THEN: the original state is saved again
	_, ok = p.originalState("fan")
	assert.True(t, ok)
	assert.Equal(t, 2, p.originalStateSaves)

	test.stop()
	_, ok = p.originalState("fan")
	assert.False(t, ok)
}

func TestFanController_SensorFailure_RestoreFallsBackToMax_KeepsSavedState(t *testing.T) {
	// GIVEN: a fan whose original mode is manual PWM, so restoring doesn't hand it over
	fan := newPanickingFan()
	fan.ControlMode = fans.ControlModePWM
	p := newFakePersistence()
	test := startOriginalStateRun(t, fan, p, configuration.OnSensorFailureRestore)

	// WHEN: the curve fails
	test.curve.set(0, errStale)
	test.awaitPwm(255)

	// THEN: fan2go still controls the fan, at full speed
	_, ok := p.originalState("fan")
	assert.True(t, ok)
	assert.Equal(t, 0, p.originalStateDeletes)

	test.stop()
	_, ok = p.originalState("fan")
	assert.False(t, ok)
}

func TestRunInitializationAndRestore_SavesAndDeletesOriginalState(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN
	log := &writeLog{}
	fan := newInitTestFan("fan", log)
	p := newFakePersistence()
	controller := newInitTestController(p, fan)

	// WHEN: e.g. `fan2go fan init`
	_, err := controller.RunInitializationAndRestore(context.Background())

	// THEN: the state was saved while fan2go controlled the fan, and deleted after the restore
	require.NoError(t, err)
	assert.Equal(t, 1, p.originalStateSaves)
	assert.Equal(t, 1, p.originalStateDeletes)
	_, ok := p.originalState("fan")
	assert.False(t, ok)
	assert.Equal(t, fans.ControlModeAutomatic, fan.ControlMode)
}

// newHwMonFanWithPwmEnable returns a HwMonFan whose pwm1 and pwm1_enable are files in a temp dir.
func newHwMonFanWithPwmEnable(t *testing.T, pwmEnable string, pwm string) (*fans.HwMonFan, string) {
	fan, pwmEnablePath := newFileBackedHwMonFan(t, pwmEnable, nil)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(pwmEnablePath), "pwm1"), []byte(pwm), 0o644))
	fan.Config.HwMon.Platform = "nct6798"
	fan.Config.HwMon.PwmChannel = 1
	return fan, pwmEnablePath
}

func TestOriginalState_HwMonFan_SavesTheDriverValueOfTheAutomaticMode(t *testing.T) {
	// GIVEN: a hwmon fan in automatic mode 5 (e.g. "Smart Fan IV" of nct6775)
	fan, _ := newHwMonFanWithPwmEnable(t, "5", "120")
	p := newFakePersistence()
	controller := &DefaultFanController{fan: fan, persistence: p}

	// WHEN
	require.NoError(t, controller.storeInitialFanState())
	controller.markMayWriteToFan()

	// THEN
	saved, ok := p.originalState("hwmon_fan")
	require.True(t, ok)
	assert.Equal(t, persistence.FanOriginalState{
		Hardware:             "hwmon:nct6798/0/pwm1",
		ControlMode:          intPointer(int(fans.ControlModeAutomatic)),
		AutomaticControlMode: intPointer(5),
		Pwm:                  intPointer(120),
	}, saved)
}

func TestOriginalState_HwMonFan_RestoresTheSavedDriverValueOfTheAutomaticMode(t *testing.T) {
	// GIVEN: a hwmon fan left in manual mode by a killed run, whose original mode was pwm_enable=5
	fan, pwmEnablePath := newHwMonFanWithPwmEnable(t, "1", "80")
	p := newFakePersistence()
	p.originalStates["hwmon_fan"] = persistence.FanOriginalState{
		Hardware:             "hwmon:nct6798/0/pwm1",
		ControlMode:          intPointer(int(fans.ControlModeAutomatic)),
		AutomaticControlMode: intPointer(5),
		Pwm:                  intPointer(120),
	}
	controller := &DefaultFanController{fan: fan, persistence: p}

	// WHEN
	require.NoError(t, controller.storeInitialFanState())
	controller.restoreControlModeOnce()

	// THEN: pwm_enable is 5 again (not 2, which is another mode on nct6775)
	assert.Equal(t, "5", readPwmEnableFile(t, pwmEnablePath))
	_, ok := p.originalState("hwmon_fan")
	assert.False(t, ok)
}

func TestOriginalStateHardwareKey(t *testing.T) {
	assert.Equal(t, "hwmon:nct6798/1/pwm2", originalStateHardwareKey(configuration.FanConfig{
		HwMon: &configuration.HwMonFanConfig{Platform: "nct6798", Index: 1, PwmChannel: 2, SysfsPath: "/sys/class/hwmon/hwmon3"},
	}))
	assert.Equal(t, "nvidia:nvidia-10DE2489-0800/0", originalStateHardwareKey(configuration.FanConfig{
		Nvidia: &configuration.NvidiaFanConfig{Device: "nvidia-10DE2489-0800"},
	}))
	assert.Equal(t, "file:/tmp/pwm", originalStateHardwareKey(configuration.FanConfig{
		File: &configuration.FileFanConfig{Path: "/tmp/pwm"},
	}))
	assert.Equal(t, "cmd:/usr/bin/set --fan 1", originalStateHardwareKey(configuration.FanConfig{
		Cmd: &configuration.CmdFanConfig{SetPwm: &configuration.ExecConfig{Exec: "/usr/bin/set", Args: []string{"--fan", "1"}}},
	}))
}
