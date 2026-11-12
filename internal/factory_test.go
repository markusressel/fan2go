package internal

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller/status"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/statistics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exportedIds returns the values of the id label of the given metric in the default Prometheus registry.
func exportedIds(t *testing.T, metric string) []string {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	var ids []string
	for _, family := range families {
		if family.GetName() != metric {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "id" {
					ids = append(ids, label.GetValue())
				}
			}
		}
	}
	return ids
}

// newFactoryTestRegistry returns a registry with a file sensor and a file fan (both with the given id),
// and a linear curve "curve" for them.
func newFactoryTestRegistry(t *testing.T, id string) (*registry.Registry, map[configuration.FanConfig]fans.Fan) {
	t.Helper()
	dir := t.TempDir()
	sensorPath := filepath.Join(dir, "sensor")
	require.NoError(t, os.WriteFile(sensorPath, []byte("45000"), 0o644))
	reg := registry.NewRegistry()
	require.NoError(t, initializeSensors(nil, reg, []configuration.SensorConfig{
		{ID: id, File: &configuration.FileSensorConfig{Path: sensorPath}},
	}))
	require.NoError(t, initializeCurves(reg, []configuration.CurveConfig{
		{ID: "curve", Linear: &configuration.LinearCurveConfig{Sensor: id, Min: 30, Max: 80}},
	}))
	fanConfig := configuration.FanConfig{ID: id, Curve: "curve", File: &configuration.FileFanConfig{Path: filepath.Join(dir, "pwm")}}
	fan, err := fans.NewFan(fanConfig)
	require.NoError(t, err)
	reg.RegisterFan(fan)
	return reg, map[configuration.FanConfig]fans.Fan{fanConfig: fan}
}

// A reload that fails while creating the new objects restarts the old fan controllers,
// so the statistics must keep exporting the old objects.
func TestInitializeFanControllers_ReplacesCollectorsOnlyOnSuccess(t *testing.T) {
	setUpSensorFailureTestConfig(t)
	configuration.CurrentConfig.FanController.AdjustmentTickRate = time.Second
	t.Cleanup(statistics.UnregisterAll)

	// GIVEN: the objects of the current config
	oldReg, oldFans := newFactoryTestRegistry(t, "old")
	_, _, err := initializeFanControllers(nil, oldFans, oldReg)
	require.NoError(t, err)
	assert.Equal(t, []string{"old"}, exportedIds(t, "fan2go_sensor_read_failures_total"))
	assert.Equal(t, []string{"old"}, exportedIds(t, "fan2go_controller_minPwm_offset"))
	fanController, ok := oldReg.GetFanController("old")
	require.True(t, ok)
	assert.Equal(t, status.StateStopped, fanController.GetStatus().State)

	// WHEN: creating the fan controllers of a new config fails (here: a curve dependency cycle)
	newReg, newFans := newFactoryTestRegistry(t, "new")
	for _, config := range []configuration.CurveConfig{
		{ID: "a", Function: &configuration.FunctionCurveConfig{Type: configuration.FunctionMaximum, Curves: []string{"b", "b"}}},
		{ID: "b", Function: &configuration.FunctionCurveConfig{Type: configuration.FunctionMaximum, Curves: []string{"a", "a"}}},
	} {
		curve, err := curves.NewSpeedCurve(config)
		require.NoError(t, err)
		newReg.RegisterCurve(curve)
	}
	_, _, err = initializeFanControllers(nil, newFans, newReg)
	require.Error(t, err)

	// THEN: the statistics still export the old objects
	assert.Equal(t, []string{"old"}, exportedIds(t, "fan2go_sensor_read_failures_total"))
	assert.Equal(t, []string{"old"}, exportedIds(t, "fan2go_controller_minPwm_offset"))

	// WHEN: the next reload succeeds, the new objects replace them
	newReg, newFans = newFactoryTestRegistry(t, "new")
	_, _, err = initializeFanControllers(nil, newFans, newReg)
	require.NoError(t, err)
	assert.Equal(t, []string{"new"}, exportedIds(t, "fan2go_sensor_read_failures_total"))
	assert.Equal(t, []string{"new"}, exportedIds(t, "fan2go_controller_minPwm_offset"))
}

func TestInitializeSensors_HwmonSensorHasTheDetectedLabelChannelMinAndMax(t *testing.T) {
	setUpSensorFailureTestConfig(t)
	input := filepath.Join(t.TempDir(), "temp2_input")
	require.NoError(t, os.WriteFile(input, []byte("45000"), 0o644))
	controllers := []*hwmon.HwMonController{{
		Platform: "coretemp",
		Sensors: map[int]*sensors.HwmonSensor{
			2: {Label: "Core 0", Index: 1, Channel: 2, Input: input, Max: 100000, Min: -1},
		},
	}}
	reg := registry.NewRegistry()

	// WHEN
	require.NoError(t, initializeSensors(controllers, reg, []configuration.SensorConfig{
		{ID: "cpu", HwMon: &configuration.HwMonSensorConfig{Platform: "coretemp", Index: 1}},
	}))

	// THEN
	sensor, ok := reg.GetSensor("cpu")
	require.True(t, ok)
	hwmonSensor := sensor.(*sensors.HwmonSensor)
	assert.Equal(t, "Core 0", hwmonSensor.GetLabel())
	assert.Equal(t, 1, hwmonSensor.Index)
	assert.Equal(t, 2, hwmonSensor.Channel)
	assert.Equal(t, input, hwmonSensor.Input)
	assert.Equal(t, 100000, hwmonSensor.Max)
	assert.Equal(t, -1, hwmonSensor.Min)
	assert.Equal(t, 45000.0, hwmonSensor.GetMovingAvg())
}
