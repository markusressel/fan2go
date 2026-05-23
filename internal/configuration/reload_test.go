package configuration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalValidConfigYAML returns YAML text for the smallest valid fan2go configuration
// that uses only file-based sensors and fans (no hardware dependencies).
func minimalValidConfigYAML(sensorPath, fanPath string) string {
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

// minimalValidPidConfigYAML is like minimalValidConfigYAML but with a PID curve.
func minimalValidPidConfigYAML(sensorPath, fanPath string, setPoint float64) string {
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

func writeConfigFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "fan2go.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestReadAndValidateConfig_ValidLinearConfig verifies that a well-formed config file is
// parsed and validated without error, and that the returned Configuration holds the
// expected values.
func TestReadAndValidateConfig_ValidLinearConfig(t *testing.T) {
	dir := t.TempDir()
	sensorPath := filepath.Join(dir, "temp_input")
	fanPath := filepath.Join(dir, "pwm1")
	cfgPath := writeConfigFile(t, dir, minimalValidConfigYAML(sensorPath, fanPath))

	cfg, err := ReadAndValidateConfig(cfgPath)

	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Len(t, cfg.Sensors, 1)
	assert.Equal(t, "sensor1", cfg.Sensors[0].ID)
	assert.Len(t, cfg.Curves, 1)
	assert.Equal(t, "curve1", cfg.Curves[0].ID)
	assert.Len(t, cfg.Fans, 1)
	assert.Equal(t, "fan1", cfg.Fans[0].ID)
}

// TestReadAndValidateConfig_ValidPidConfig verifies that a PID curve config is parsed
// correctly and the SetPoint value is preserved.
func TestReadAndValidateConfig_ValidPidConfig(t *testing.T) {
	dir := t.TempDir()
	sensorPath := filepath.Join(dir, "temp_input")
	fanPath := filepath.Join(dir, "pwm1")
	const wantSetPoint = 65.5
	cfgPath := writeConfigFile(t, dir, minimalValidPidConfigYAML(sensorPath, fanPath, wantSetPoint))

	cfg, err := ReadAndValidateConfig(cfgPath)

	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Curves, 1)
	require.NotNil(t, cfg.Curves[0].PID)
	assert.Equal(t, wantSetPoint, cfg.Curves[0].PID.SetPoint)
}

// TestReadAndValidateConfig_InvalidYAML verifies that malformed YAML (unclosed bracket)
// is rejected with an error.
func TestReadAndValidateConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	// Unclosed bracket is a guaranteed YAML parse error.
	cfgPath := writeConfigFile(t, dir, "sensors: [unclosed")

	cfg, err := ReadAndValidateConfig(cfgPath)

	assert.Error(t, err)
	assert.Nil(t, cfg)
}

// TestReadAndValidateConfig_ValidationFailure verifies that structurally valid YAML that
// fails semantic validation (e.g. referencing a non-existent sensor) is rejected.
func TestReadAndValidateConfig_ValidationFailure(t *testing.T) {
	dir := t.TempDir()
	fanPath := filepath.Join(dir, "pwm1")
	// curve references a sensor that is not declared
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
	cfgPath := writeConfigFile(t, dir, yaml)

	cfg, err := ReadAndValidateConfig(cfgPath)

	assert.Error(t, err)
	assert.Nil(t, cfg)
}

// TestReadAndValidateConfig_DoesNotModifyCurrentConfig verifies that a successful read
// does not change the global CurrentConfig.
func TestReadAndValidateConfig_DoesNotModifyCurrentConfig(t *testing.T) {
	dir := t.TempDir()
	sensorPath := filepath.Join(dir, "temp_input")
	fanPath := filepath.Join(dir, "pwm1")
	cfgPath := writeConfigFile(t, dir, minimalValidConfigYAML(sensorPath, fanPath))

	before := CurrentConfig // snapshot before

	_, err := ReadAndValidateConfig(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, before, CurrentConfig, "ReadAndValidateConfig must not mutate CurrentConfig")
}

// TestReadAndValidateConfig_NonExistentFile verifies that a missing config path is
// reported as an error.
func TestReadAndValidateConfig_NonExistentFile(t *testing.T) {
	cfg, err := ReadAndValidateConfig("/no/such/file/fan2go.yaml")

	assert.Error(t, err)
	assert.Nil(t, cfg)
}
