package reload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Minimal fakes used only within this package's tests
// ---------------------------------------------------------------------------

// fakeFan implements fans.Fan with just enough for the reload tests.
type fakeFan struct {
	id      string
	curveID string
}

func (f *fakeFan) GetId() string                                        { return f.id }
func (f *fakeFan) GetCurveId() string                                   { return f.curveID }
func (f *fakeFan) GetMinPwm() int                                       { return 0 }
func (f *fakeFan) SetMinPwm(_ int, _ bool)                              {}
func (f *fakeFan) GetStartPwm() int                                     { return 0 }
func (f *fakeFan) SetStartPwm(_ int, _ bool)                            {}
func (f *fakeFan) GetMaxPwm() int                                       { return 255 }
func (f *fakeFan) SetMaxPwm(_ int, _ bool)                              {}
func (f *fakeFan) GetRpm() (int, error)                                 { return 0, nil }
func (f *fakeFan) GetRpmAvg() float64                                   { return 0 }
func (f *fakeFan) SetRpmAvg(_ float64)                                  {}
func (f *fakeFan) GetPwm() (int, error)                                 { return 0, nil }
func (f *fakeFan) SetPwm(_ int) error                                   { return nil }
func (f *fakeFan) GetFanRpmCurveData() *map[int]float64                 { return nil }
func (f *fakeFan) AttachFanRpmCurveData(_ *map[int]float64) error       { return nil }
func (f *fakeFan) UpdateFanRpmCurveValue(_ int, _ float64)              {}
func (f *fakeFan) ShouldNeverStop() bool                                { return false }
func (f *fakeFan) GetControlMode() (fans.ControlMode, error)            { return 0, nil }
func (f *fakeFan) SetControlMode(_ fans.ControlMode) error              { return nil }
func (f *fakeFan) GetConfig() configuration.FanConfig                   { return configuration.FanConfig{} }
func (f *fakeFan) SetConfig(_ configuration.FanConfig)                  {}
func (f *fakeFan) GetLabel() string                                     { return f.id }
func (f *fakeFan) GetIndex() int                                        { return 1 }
func (f *fakeFan) Supports(_ fans.FeatureFlag) bool                     { return false }

// fakeController implements controller.FanController and records the most
// recently set curve so tests can inspect it.
type fakeController struct {
	currentCurve curves.SpeedCurve
}

func (c *fakeController) Run(_ context.Context) error         { return nil }
func (c *fakeController) GetFanId() string                    { return "fan1" }
func (c *fakeController) GetStatistics() controller.FanControllerStatistics {
	return controller.FanControllerStatistics{}
}
func (c *fakeController) UpdateFanSpeed() error              { return nil }
func (c *fakeController) SetCurve(curve curves.SpeedCurve)   { c.currentCurve = curve }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeYAML(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "fan2go.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func linearYAML(sensorPath, fanPath string) string {
	return fmt.Sprintf(`
sensors:
  - id: sensor1
    file:
      path: %s
curves:
  - id: curve1
    linear:
      sensor: sensor1
      min: 40
      max: 80
fans:
  - id: fan1
    file:
      path: %s
    curve: curve1
`, sensorPath, fanPath)
}

func pidYAML(sensorPath, fanPath string, setPoint float64) string {
	return fmt.Sprintf(`
sensors:
  - id: sensor1
    file:
      path: %s
curves:
  - id: pid_curve1
    pid:
      sensor: sensor1
      setPoint: %.1f
      p: -0.05
      i: -0.005
      d: -0.005
fans:
  - id: fan1
    file:
      path: %s
    curve: pid_curve1
`, sensorPath, setPoint, fanPath)
}

// ---------------------------------------------------------------------------
// Tests for applyNewConfig
// ---------------------------------------------------------------------------

// TestApplyNewConfig_UpdatesCurrentConfig verifies that applyNewConfig replaces the
// global CurrentConfig with the supplied configuration.
func TestApplyNewConfig_UpdatesCurrentConfig(t *testing.T) {
	dir := t.TempDir()
	sensorPath := filepath.Join(dir, "temp_input")
	fanPath := filepath.Join(dir, "pwm1")
	cfgPath := writeYAML(t, dir, linearYAML(sensorPath, fanPath))

	newCfg, err := configuration.ReadAndValidateConfig(cfgPath)
	require.NoError(t, err)

	rm := NewReloadManager(cfgPath, nil)
	rm.applyNewConfig(newCfg)

	assert.Equal(t, "sensor1", configuration.CurrentConfig.Sensors[0].ID)
	assert.Equal(t, "curve1", configuration.CurrentConfig.Curves[0].ID)
	assert.Equal(t, "fan1", configuration.CurrentConfig.Fans[0].ID)
}

// TestApplyNewConfig_RecreatesSpeedCurves verifies that existing speed curve objects in
// the global registry are replaced with newly constructed ones after applyNewConfig.
func TestApplyNewConfig_RecreatesSpeedCurves(t *testing.T) {
	dir := t.TempDir()
	sensorPath := filepath.Join(dir, "temp_input")
	fanPath := filepath.Join(dir, "pwm1")
	cfgPath := writeYAML(t, dir, linearYAML(sensorPath, fanPath))

	newCfg, err := configuration.ReadAndValidateConfig(cfgPath)
	require.NoError(t, err)

	// Pre-register an "old" curve with a recognisable value.
	oldCurve, _ := curves.NewSpeedCurve(newCfg.Curves[0])
	oldCurve.(*curves.LinearSpeedCurve).Value = 42
	curves.RegisterSpeedCurve(oldCurve)

	rm := NewReloadManager(cfgPath, nil)
	rm.applyNewConfig(newCfg)

	got, found := curves.GetSpeedCurve("curve1")
	require.True(t, found)
	// The new curve should be a fresh instance with zero value.
	assert.Equal(t, 0.0, got.CurrentValue(), "newly created curve should start with value 0")
}

// TestApplyNewConfig_UpdatesFanControllerCurve verifies that each fan controller receives
// the rebuilt SpeedCurve via SetCurve after applyNewConfig.
func TestApplyNewConfig_UpdatesFanControllerCurve(t *testing.T) {
	dir := t.TempDir()
	sensorPath := filepath.Join(dir, "temp_input")
	fanPath := filepath.Join(dir, "pwm1")
	cfgPath := writeYAML(t, dir, linearYAML(sensorPath, fanPath))

	newCfg, err := configuration.ReadAndValidateConfig(cfgPath)
	require.NoError(t, err)

	fan := &fakeFan{id: "fan1", curveID: "curve1"}
	ctrl := &fakeController{}
	fanControllers := map[fans.Fan]controller.FanController{fan: ctrl}

	rm := NewReloadManager(cfgPath, fanControllers)
	rm.applyNewConfig(newCfg)

	assert.NotNil(t, ctrl.currentCurve, "controller should have received a new curve")
	assert.Equal(t, "curve1", ctrl.currentCurve.GetId())
}

// ---------------------------------------------------------------------------
// Tests for reload (full parse → validate → apply pipeline)
// ---------------------------------------------------------------------------

// TestReload_ValidConfig verifies that reload() with a valid config file updates
// CurrentConfig and the fan controller's curve.
func TestReload_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	sensorPath := filepath.Join(dir, "temp_input")
	fanPath := filepath.Join(dir, "pwm1")
	cfgPath := writeYAML(t, dir, linearYAML(sensorPath, fanPath))

	// Reset global state before the test.
	configuration.CurrentConfig = configuration.Configuration{}

	fan := &fakeFan{id: "fan1", curveID: "curve1"}
	ctrl := &fakeController{}
	fanControllers := map[fans.Fan]controller.FanController{fan: ctrl}

	rm := NewReloadManager(cfgPath, fanControllers)
	rm.reload()

	assert.Equal(t, "fan1", configuration.CurrentConfig.Fans[0].ID)
	assert.NotNil(t, ctrl.currentCurve)
}

// TestReload_InvalidConfig verifies that reload() with a malformed YAML file leaves
// CurrentConfig unchanged.
func TestReload_InvalidConfig(t *testing.T) {
	dir := t.TempDir()
	// Unclosed bracket is a guaranteed YAML parse error.
	cfgPath := writeYAML(t, dir, "sensors: [unclosed")

	// Set a sentinel value so we can detect unwanted modification.
	configuration.CurrentConfig = configuration.Configuration{DbPath: "sentinel-value"}

	rm := NewReloadManager(cfgPath, nil)
	rm.reload()

	assert.Equal(t, "sentinel-value", configuration.CurrentConfig.DbPath,
		"invalid reload must not change CurrentConfig")
}

// TestReload_ValidationFailure verifies that reload() with syntactically valid but
// semantically invalid config (missing sensor reference) is rejected and CurrentConfig
// is not modified.
func TestReload_ValidationFailure(t *testing.T) {
	dir := t.TempDir()
	fanPath := filepath.Join(dir, "pwm1")
	yaml := fmt.Sprintf(`
sensors:
  - id: sensor1
    file:
      path: /tmp/sensor
curves:
  - id: curve1
    linear:
      sensor: nonexistent_sensor
      min: 40
      max: 80
fans:
  - id: fan1
    file:
      path: %s
    curve: curve1
`, fanPath)
	cfgPath := writeYAML(t, dir, yaml)

	configuration.CurrentConfig = configuration.Configuration{}
	before := configuration.CurrentConfig

	rm := NewReloadManager(cfgPath, nil)
	rm.reload()

	assert.Equal(t, before, configuration.CurrentConfig, "rejected reload must not change CurrentConfig")
}

// ---------------------------------------------------------------------------
// PID setpoint update test
// ---------------------------------------------------------------------------

// TestReload_PidSetpointUpdate verifies the complete hot-reload path for a PID curve:
// after reloading a config with a different setPoint the fan controller's assigned
// curve reflects the new value.
func TestReload_PidSetpointUpdate(t *testing.T) {
	dir := t.TempDir()
	sensorPath := filepath.Join(dir, "temp_input")
	fanPath := filepath.Join(dir, "pwm1")

	// Write initial config with setPoint 60.
	cfgPath := writeYAML(t, dir, pidYAML(sensorPath, fanPath, 60.0))

	fan := &fakeFan{id: "fan1", curveID: "pid_curve1"}
	ctrl := &fakeController{}
	fanControllers := map[fans.Fan]controller.FanController{fan: ctrl}

	rm := NewReloadManager(cfgPath, fanControllers)
	rm.reload()

	require.NotNil(t, ctrl.currentCurve)
	pidCurve1, ok := ctrl.currentCurve.(*curves.PidSpeedCurve)
	require.True(t, ok, "expected *PidSpeedCurve")
	assert.Equal(t, 60.0, pidCurve1.Config.PID.SetPoint)

	// Now update the config file with setPoint 70 and reload.
	require.NoError(t, os.WriteFile(cfgPath, []byte(pidYAML(sensorPath, fanPath, 70.0)), 0o600))
	rm.reload()

	require.NotNil(t, ctrl.currentCurve)
	pidCurve2, ok := ctrl.currentCurve.(*curves.PidSpeedCurve)
	require.True(t, ok, "expected *PidSpeedCurve")
	assert.Equal(t, 70.0, pidCurve2.Config.PID.SetPoint)
	// The new curve is a fresh instance (state was reset as documented).
	assert.NotSame(t, pidCurve1, pidCurve2, "reload should produce a new curve instance")
}

// ---------------------------------------------------------------------------
// Run lifecycle test
// ---------------------------------------------------------------------------

// TestReloadManager_RunStopsOnContextCancel verifies that Run returns promptly when
// the supplied context is cancelled (i.e. the daemon is shutting down).
func TestReloadManager_RunStopsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeYAML(t, dir, "# placeholder")

	rm := NewReloadManager(cfgPath, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rm.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
