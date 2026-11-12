package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restoreTestFan is a fan with configurable features and read/write failures,
// which records every PWM and control mode write.
type restoreTestFan struct {
	MockFan
	features          map[fans.FeatureFlag]bool
	pwmReadErr        error
	controlModeErr    error
	setControlModeErr error
	// failingControlModes lists control modes whose write fails with an error
	failingControlModes map[fans.ControlMode]bool
	pwmWrites           []int
	controlModeWrites   []fans.ControlMode
}

// GetId only reads the ID: the promoted MockFan.GetId copies the whole struct, which races with
// writes of the update loop.
func (fan *restoreTestFan) GetId() string {
	return fan.ID
}

func (fan *restoreTestFan) Supports(feature fans.FeatureFlag) bool {
	return fan.features[feature]
}

func (fan *restoreTestFan) GetPwm() (int, error) {
	if fan.pwmReadErr != nil {
		// like HwMonFan, return a bogus value together with the error
		return fans.MinPwmValue, fan.pwmReadErr
	}
	return fan.PWM, nil
}

func (fan *restoreTestFan) SetPwm(pwm int) error {
	fan.pwmWrites = append(fan.pwmWrites, pwm)
	fan.PWM = pwm
	return nil
}

func (fan *restoreTestFan) GetControlMode() (fans.ControlMode, error) {
	if fan.controlModeErr != nil {
		return fans.ControlModeUnknown, fan.controlModeErr
	}
	return fan.ControlMode, nil
}

func (fan *restoreTestFan) SetControlMode(mode fans.ControlMode) error {
	fan.controlModeWrites = append(fan.controlModeWrites, mode)
	if fan.setControlModeErr != nil {
		return fan.setControlModeErr
	}
	if fan.failingControlModes[mode] {
		return errors.New("control mode rejected")
	}
	fan.ControlMode = mode
	return nil
}

var allFanFeatures = map[fans.FeatureFlag]bool{
	fans.FeaturePwmSensor:        true,
	fans.FeatureRpmSensor:        true,
	fans.FeatureControlModeRead:  true,
	fans.FeatureControlModeWrite: true,
}

// newPwmMappedController returns a controller whose pwmMap was computed from the given step pwmMap.
func newPwmMappedController(t *testing.T, fan *restoreTestFan, pwmMap map[int]int) *DefaultFanController {
	values := configuration.PwmMapValuesConfig(pwmMap)
	fan.PwmMap = &configuration.PwmMapConfig{Values: &values}
	controller := &DefaultFanController{fan: fan}
	require.NoError(t, controller.computePwmMap())
	controller.pwmMapComputed = true
	return controller
}

func TestRestoreControlMode_NoPwmReadbackAndNoControlModeRead_WritesNothing(t *testing.T) {
	// GIVEN: a fan without PWM readback and without control mode support (e.g. a cmd fan without getPwm),
	// running at PWM 180 before fan2go started
	fan := &restoreTestFan{
		MockFan:  MockFan{ID: "fan", PWM: 180, MinPWM: 0},
		features: map[fans.FeatureFlag]bool{},
	}
	controller := DefaultFanController{fan: fan}

	// WHEN
	require.NoError(t, controller.storeCurrentFanState())
	controller.restoreControlMode()

	// THEN: neither the PWM value nor the control mode is known, so nothing is written
	assert.Nil(t, controller.originalFanState.PwmValue)
	assert.Nil(t, controller.originalFanState.ControlMode)
	assert.Empty(t, fan.pwmWrites, "an unknown original PWM must not be restored")
	assert.Empty(t, fan.controlModeWrites, "an unknown control mode must not be restored")
}

func TestRestoreControlMode_PwmReadFailsDuringSnapshot_DoesNotRestorePwm(t *testing.T) {
	// GIVEN: a fan in manual mode whose PWM read fails when the snapshot is taken
	fan := &restoreTestFan{
		MockFan:    MockFan{ID: "fan", PWM: 180, ControlMode: fans.ControlModePWM},
		features:   allFanFeatures,
		pwmReadErr: errors.New("transient read error"),
	}
	controller := DefaultFanController{fan: fan}

	// WHEN
	require.NoError(t, controller.storeCurrentFanState())
	controller.restoreControlMode()

	// THEN: the known control mode is restored, the value returned with the error is not
	assert.Nil(t, controller.originalFanState.PwmValue)
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM}, fan.controlModeWrites)
	assert.Empty(t, fan.pwmWrites)
}

// newUnknownControlModeController returns a controller for a fan that can write its control mode,
// but whose control mode couldn't be read when the snapshot was taken.
func newUnknownControlModeController(t *testing.T, fan *restoreTestFan) *DefaultFanController {
	fan.features = allFanFeatures
	fan.controlModeErr = errors.New("read error")
	controller := &DefaultFanController{fan: fan}
	require.NoError(t, controller.storeCurrentFanState())
	require.Nil(t, controller.originalFanState.ControlMode)
	return controller
}

func TestRestoreControlMode_UnknownControlMode_AutomaticSucceeds_NoPwmWrite(t *testing.T) {
	// GIVEN
	fan := &restoreTestFan{MockFan: MockFan{ID: "fan", PWM: 180}}
	controller := newUnknownControlModeController(t, fan)

	// WHEN
	controller.restoreControlMode()

	// THEN: the fan is handed back to automatic control, ControlModeUnknown is never written,
	// and no PWM is written afterwards (on some drivers that switches back to manual control)
	assert.Equal(t, []fans.ControlMode{fans.ControlModeAutomatic}, fan.controlModeWrites)
	assert.Empty(t, fan.pwmWrites)
}

func TestRestoreControlMode_UnknownControlMode_AutomaticFails_RestoresOriginalPwmInManualMode(t *testing.T) {
	// GIVEN
	fan := &restoreTestFan{
		MockFan:             MockFan{ID: "fan", PWM: 180},
		failingControlModes: map[fans.ControlMode]bool{fans.ControlModeAutomatic: true},
	}
	controller := newUnknownControlModeController(t, fan)

	// WHEN
	controller.restoreControlMode()

	// THEN
	assert.Equal(t, []fans.ControlMode{fans.ControlModeAutomatic, fans.ControlModePWM}, fan.controlModeWrites)
	assert.Equal(t, []int{180}, fan.pwmWrites)
}

func TestRestoreControlMode_UnknownControlMode_AutomaticFailsAndPwmUnknown_WritesNothingElse(t *testing.T) {
	// GIVEN
	fan := &restoreTestFan{
		MockFan:             MockFan{ID: "fan", PWM: 180},
		failingControlModes: map[fans.ControlMode]bool{fans.ControlModeAutomatic: true},
		pwmReadErr:          errors.New("read error"),
	}
	controller := newUnknownControlModeController(t, fan)

	// WHEN
	controller.restoreControlMode()

	// THEN: only the attempt to switch to automatic control, no made-up PWM value
	assert.Equal(t, []fans.ControlMode{fans.ControlModeAutomatic}, fan.controlModeWrites)
	assert.Empty(t, fan.pwmWrites)
}

func TestRestoreControlMode_UnknownControlMode_OnExitSettingsTakePrecedence(t *testing.T) {
	// GIVEN: onExit.speed is configured
	speed := 100
	fan := &restoreTestFan{MockFan: MockFan{
		ID:                "fan",
		PWM:               180,
		ControlModeConfig: &configuration.ControlModeConfig{OnExit: &configuration.OnExitConfig{Speed: &speed}},
	}}
	controller := newUnknownControlModeController(t, fan)

	// WHEN
	controller.restoreControlMode()

	// THEN: no switch to automatic control, only the configured speed
	assert.Empty(t, fan.controlModeWrites)
	assert.Equal(t, []int{100}, fan.pwmWrites)
}

func TestRestoreControlMode_HwMonPwmEnableNotReadable_SwitchesToAutomatic(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read files without read permission")
	}
	// GIVEN: a hwmon fan whose pwm_enable can be written, but not read
	fan, pwmEnablePath := newFileBackedHwMonFan(t, "1", nil)
	pwmPath := fan.Config.HwMon.PwmPath
	require.NoError(t, os.WriteFile(pwmPath, []byte("180"), 0o644))
	require.NoError(t, os.Chmod(pwmEnablePath, 0o200))
	controller := DefaultFanController{fan: fan}
	require.NoError(t, controller.storeCurrentFanState())
	require.Nil(t, controller.originalFanState.ControlMode)
	require.NoError(t, os.WriteFile(pwmPath, []byte("50"), 0o644)) // fan2go controls the fan

	// WHEN
	controller.restoreControlMode()

	// THEN: the write without readback counts as success, so no PWM is written afterwards
	require.NoError(t, os.Chmod(pwmEnablePath, 0o644))
	assert.Equal(t, "2", readPwmEnableFile(t, pwmEnablePath))
	pwm, err := os.ReadFile(pwmPath)
	require.NoError(t, err)
	assert.Equal(t, "50", strings.TrimSpace(string(pwm)))
}

func TestRestoreControlMode_PwmReadbackWithoutControlMode_RestoresPwm(t *testing.T) {
	// GIVEN: a fan with PWM readback but without control mode support (e.g. a file fan)
	fan := &restoreTestFan{
		MockFan:  MockFan{ID: "fan", PWM: 180},
		features: map[fans.FeatureFlag]bool{fans.FeaturePwmSensor: true},
	}
	controller := DefaultFanController{fan: fan}

	// WHEN
	require.NoError(t, controller.storeCurrentFanState())
	fan.PWM = 50 // fan2go controls the fan
	controller.restoreControlMode()

	// THEN
	assert.Equal(t, []int{180}, fan.pwmWrites)
	assert.Empty(t, fan.controlModeWrites)
}

func TestRestoreControlMode_OnExitSpeed_GoesThroughPwmMap(t *testing.T) {
	// GIVEN: a fan with pwmMap {0:0, 128:3, 255:6} and onExit.speed 128
	speed := 128
	fan := &restoreTestFan{
		MockFan: MockFan{
			ID:                "fan",
			PWM:               6,
			ControlModeConfig: &configuration.ControlModeConfig{OnExit: &configuration.OnExitConfig{Speed: &speed}},
		},
		features: allFanFeatures,
	}
	controller := newPwmMappedController(t, fan, map[int]int{0: 0, 128: 3, 255: 6})
	controller.originalFanState = &FanStateSnapshot{
		PwmValue:    ptrTo(6),
		ControlMode: ptrTo(fans.ControlModePWM),
	}

	// WHEN
	controller.restoreControlMode()

	// THEN: speed 128 is mapped to the raw value 3
	assert.Equal(t, []int{3}, fan.pwmWrites)
}

func TestRestoreControlMode_OnExitSpeed_PwmMapNotComputedYet_WritesSpeedAsIs(t *testing.T) {
	// GIVEN: the controller stops before its pwmMap was computed (e.g. during initialization)
	speed := 128
	fan := &restoreTestFan{
		MockFan: MockFan{
			ID:                "fan",
			ControlModeConfig: &configuration.ControlModeConfig{OnExit: &configuration.OnExitConfig{Speed: &speed}},
		},
		features: allFanFeatures,
	}
	controller := DefaultFanController{
		fan:              fan,
		originalFanState: &FanStateSnapshot{ControlMode: ptrTo(fans.ControlModePWM)},
	}

	// WHEN
	controller.restoreControlMode()

	// THEN: the all-zero pwmMapping is not applied
	assert.Equal(t, []int{128}, fan.pwmWrites)
}

func TestRestoreControlMode_SetControlModeFails_FallbackWritesMappedMaxSpeed(t *testing.T) {
	// GIVEN: a fan with pwmMap {0:0, 128:3, 255:6} whose control mode can't be restored
	fan := &restoreTestFan{
		MockFan:           MockFan{ID: "fan", PWM: 3},
		features:          allFanFeatures,
		setControlModeErr: errors.New("write rejected"),
	}
	controller := newPwmMappedController(t, fan, map[int]int{0: 0, 128: 3, 255: 6})
	controller.originalFanState = &FanStateSnapshot{
		PwmValue:    ptrTo(3),
		ControlMode: ptrTo(fans.ControlModeAutomatic),
	}

	// WHEN
	controller.restoreControlMode()

	// THEN: the max speed fallback writes pwmMapping[255]
	assert.Equal(t, []fans.ControlMode{fans.ControlModeAutomatic}, fan.controlModeWrites)
	assert.Equal(t, []int{6}, fan.pwmWrites)
}

func TestGetPwm_NoPwmSensorAndNoTarget_ReturnsError(t *testing.T) {
	fan := &restoreTestFan{
		MockFan:  MockFan{ID: "fan", PWM: 180, MinPWM: 50},
		features: map[fans.FeatureFlag]bool{},
	}
	controller := DefaultFanController{fan: fan}

	_, err := controller.getPwm()
	assert.Error(t, err, "minPwm must not be reported as the current PWM value")

	target := 100
	controller.lastTarget = &target
	for i := range controller.pwmMapping {
		controller.pwmMapping[i] = i
	}
	pwm, err := controller.getPwm()
	assert.NoError(t, err)
	assert.Equal(t, 100, pwm)
}

func TestFanController_Run_FanWithoutPwmReadback_KeepsLastSpeedOnExit(t *testing.T) {
	// GIVEN: a fan without PWM readback and without control mode support (e.g. a cmd fan without getPwm)
	originalConfig := configuration.CurrentConfig
	defer func() {
		configuration.CurrentConfig = originalConfig
	}()
	configuration.CurrentConfig.TempSensorPollingRate = 0

	curveValue := 255.0
	clock := util.NewFakeClock(time.Unix(1000, 0))
	fan := &restoreTestFan{
		MockFan:  MockFan{ID: "fan", PWM: 180},
		features: map[fans.FeatureFlag]bool{},
	}
	controller := DefaultFanController{
		persistence:         mockPersistence{hasSavedPwmData: true},
		fan:                 fan,
		curve:               MockCurve{ID: "curve", Value: &curveValue},
		updateRate:          time.Hour,
		controlLoop:         control_loop.NewDirectControlLoop(nil),
		clock:               clock,
		startupSensorWarmup: defaultStartupSensorWarmup,
		startupUpdateDelay:  defaultStartupUpdateDelay,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- controller.Run(ctx)
	}()
	clock.BlockUntil(1)
	clock.Advance(defaultStartupSensorWarmup)
	clock.BlockUntil(1)
	clock.Advance(defaultStartupUpdateDelay)
	// the update loop creates its ticker right before the first update
	clock.BlockUntil(1)

	// WHEN
	cancel()

	// THEN: only the first update wrote to the fan, the restore on exit didn't
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after its context was cancelled")
	}
	assert.Equal(t, []int{255}, fan.pwmWrites)
	assert.Empty(t, fan.controlModeWrites)
}

// newFileBackedHwMonFan returns a HwMonFan whose pwm1_enable is a file in a temp dir.
func newFileBackedHwMonFan(t *testing.T, initialPwmEnable string, cfg *configuration.ControlModeConfig) (*fans.HwMonFan, string) {
	dir := t.TempDir()
	pwmEnablePath := filepath.Join(dir, "pwm1_enable")
	require.NoError(t, os.WriteFile(pwmEnablePath, []byte(initialPwmEnable), 0o644))
	fan := &fans.HwMonFan{
		Config: configuration.FanConfig{
			ID:          "hwmon_fan",
			ControlMode: cfg,
			HwMon: &configuration.HwMonFanConfig{
				PwmEnablePath: pwmEnablePath,
				PwmPath:       filepath.Join(dir, "pwm1"),
				RpmInputPath:  filepath.Join(dir, "fan1_input"),
			},
		},
	}
	return fan, pwmEnablePath
}

func readPwmEnableFile(t *testing.T, path string) string {
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.TrimSpace(string(content))
}

func TestTrySetManualPwm_IntegerControlMode_WritesValueToHwMonPwmEnable(t *testing.T) {
	// GIVEN: controlMode.active: "3" on a hwmon fan
	active := configuration.ControlModeValue("3")
	fan, pwmEnablePath := newFileBackedHwMonFan(t, "5", &configuration.ControlModeConfig{Active: &active})

	// WHEN
	err := trySetManualPwm(fan)

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, "3", readPwmEnableFile(t, pwmEnablePath))
}

func TestHwMonFan_IntegerControlMode_DoesNotReplaceOriginalAutomaticMode(t *testing.T) {
	// GIVEN: a hwmon fan in automatic mode 5, which fan2go switches to the integer mode 3
	active := configuration.ControlModeValue("3")
	fan, pwmEnablePath := newFileBackedHwMonFan(t, "5", &configuration.ControlModeConfig{Active: &active})
	controller := DefaultFanController{fan: fan}
	require.NoError(t, controller.storeCurrentFanState())
	require.NoError(t, trySetManualPwm(fan))

	// a periodic control mode check reads back the mode fan2go set
	mode, err := fan.GetControlMode()
	require.NoError(t, err)
	assert.Equal(t, fans.ControlModeAutomatic, mode)

	// WHEN
	controller.restoreControlMode()

	// THEN: the original automatic mode 5 is restored, not fan2go's mode 3
	assert.Equal(t, "5", readPwmEnableFile(t, pwmEnablePath))
}

func TestHwMonFan_SetControlMode_UnknownIsNeverWritten(t *testing.T) {
	for _, mode := range []fans.ControlMode{fans.ControlModeUnknown, fans.ControlMode(-5)} {
		// GIVEN
		fan, pwmEnablePath := newFileBackedHwMonFan(t, "2", nil)

		// WHEN
		err := fan.SetControlMode(mode)

		// THEN
		assert.Error(t, err)
		assert.Equal(t, "2", readPwmEnableFile(t, pwmEnablePath), "mode %d must not be written", mode)
	}
}
