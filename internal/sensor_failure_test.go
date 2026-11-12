package internal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/statistics"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedSensor returns the results of script, one per read, and then keeps returning the last one.
type scriptedSensor struct {
	sensors.ReadingTracker

	mu        sync.Mutex
	script    []scriptedRead
	movingAvg float64
}

type scriptedRead struct {
	value float64
	err   error
}

func (s *scriptedSensor) GetId() string    { return "scripted" }
func (s *scriptedSensor) GetLabel() string { return "scripted" }
func (s *scriptedSensor) GetConfig() configuration.SensorConfig {
	return configuration.SensorConfig{ID: "scripted"}
}

func (s *scriptedSensor) GetValue() (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.script[0]
	if len(s.script) > 1 {
		s.script = s.script[1:]
	}
	return next.value, next.err
}

func (s *scriptedSensor) GetMovingAvg() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.movingAvg
}

func (s *scriptedSensor) SetMovingAvg(avg float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.movingAvg = avg
}

func setUpSensorFailureTestConfig(t *testing.T) {
	originalConfig := configuration.CurrentConfig
	t.Cleanup(func() { configuration.CurrentConfig = originalConfig })
	configuration.CurrentConfig.TempRollingWindowSize = 10
	configuration.CurrentConfig.TempSensorPollingRate = time.Second
	configuration.CurrentConfig.SensorStaleTimeout = nil
}

func TestUpdateSensor_RecordsReadingsAndFailures(t *testing.T) {
	setUpSensorFailureTestConfig(t)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	sensor := &scriptedSensor{script: []scriptedRead{
		{err: errors.New("read failed")},
		{value: 50000},
		{err: errors.New("read failed")},
		{err: errors.New("read failed")},
		{value: 60000},
	}}

	// a failed first reading leaves the sensor without a reading
	updateSensor(sensor, clock)
	_, ok := sensor.GetLastReading()
	assert.False(t, ok)
	assert.Equal(t, 1, sensor.GetConsecutiveFailures())

	// the first successful reading replaces the average, instead of averaging it with 0
	clock.Advance(time.Second)
	updateSensor(sensor, clock)
	assert.Equal(t, 50000.0, sensor.GetMovingAvg())
	reading, ok := sensor.GetLastReading()
	require.True(t, ok)
	assert.Equal(t, sensors.Reading{Value: 50000, Time: time.Unix(1001, 0)}, reading)
	assert.Equal(t, 0, sensor.GetConsecutiveFailures())

	// failed readings keep the last good reading and the average
	clock.Advance(time.Second)
	updateSensor(sensor, clock)
	clock.Advance(time.Second)
	updateSensor(sensor, clock)
	assert.Equal(t, 2, sensor.GetConsecutiveFailures())
	assert.Equal(t, 50000.0, sensor.GetMovingAvg())
	reading, _ = sensor.GetLastReading()
	assert.Equal(t, time.Unix(1001, 0), reading.Time)

	// later readings update the average and the reading
	clock.Advance(time.Second)
	updateSensor(sensor, clock)
	assert.Equal(t, util.UpdateExponentialMovingAvg(50000, 10, 60000), sensor.GetMovingAvg())
	reading, _ = sensor.GetLastReading()
	assert.Equal(t, sensors.Reading{Value: 60000, Time: time.Unix(1004, 0)}, reading)
	assert.Equal(t, 0, sensor.GetConsecutiveFailures())
}

// A sensor that fails for longer than sensorStaleTimeout makes the curves using it fail with ErrSensorStale.
func TestSensorMonitor_SensorFailingLongerThanStaleTimeout_CurveReturnsErrSensorStale(t *testing.T) {
	setUpSensorFailureTestConfig(t)
	staleTimeout := configuration.CurrentConfig.GetSensorStaleTimeout()
	require.Equal(t, 10*time.Second, staleTimeout)

	clock := util.NewFakeClock(time.Unix(1000, 0))
	sensor := &scriptedSensor{script: []scriptedRead{{value: 50000}, {err: errors.New("sensor is gone")}}}
	reg := registry.NewRegistry()
	reg.RegisterSensor(sensor)
	curve, err := curves.NewSpeedCurve(configuration.CurveConfig{
		ID: "curve",
		Linear: &configuration.LinearCurveConfig{
			Sensor: sensor.GetId(),
			Min:    40,
			Max:    80,
		},
	})
	require.NoError(t, err)
	curve.(interface{ SetClock(util.Clock) }).SetClock(clock)
	reg.RegisterCurve(curve)

	monitor := NewSensorMonitorWithClock(sensor, configuration.CurrentConfig.TempSensorPollingRate, clock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- monitor.Run(ctx) }()
	// the monitor reads the sensor once right away, then waits for its ticker
	clock.BlockUntil(1)
	require.Eventually(t, func() bool { _, ok := sensor.GetLastReading(); return ok }, 5*time.Second, time.Millisecond)

	value, err := curve.Evaluate()
	require.NoError(t, err)
	assert.InDelta(t, 255.0/4, value, 0.001)

	// WHEN: every further reading fails, until the timeout has passed
	for elapsed := time.Duration(0); elapsed < staleTimeout; elapsed += time.Second {
		failures := sensor.GetConsecutiveFailures()
		clock.Advance(time.Second)
		require.Eventually(t, func() bool { return sensor.GetConsecutiveFailures() > failures }, 5*time.Second, time.Millisecond)
	}
	// exactly at the timeout, the value is still accepted
	_, err = curve.Evaluate()
	assert.NoError(t, err)
	clock.Advance(time.Millisecond)

	// THEN
	value, err = curve.Evaluate()
	assert.ErrorIs(t, err, sensors.ErrSensorStale)
	assert.InDelta(t, 255.0/4, value, 0.001, "the curve returns its last value together with the error")

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("sensor monitor did not stop")
	}
}

func TestInitializeSensors_StartupReadFailure_DoesNotSeedZero(t *testing.T) {
	setUpSensorFailureTestConfig(t)
	t.Cleanup(statistics.UnregisterAll)
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good")
	require.NoError(t, os.WriteFile(goodPath, []byte("45000"), 0o644))
	reg := registry.NewRegistry()

	// GIVEN: one sensor that can be read at startup, and one that can't
	err := initializeSensors(nil, reg, []configuration.SensorConfig{
		{ID: "good", File: &configuration.FileSensorConfig{Path: goodPath}},
		{ID: "bad", File: &configuration.FileSensorConfig{Path: filepath.Join(dir, "missing")}},
	})
	require.NoError(t, err)

	// THEN: the readable sensor is seeded with its value
	good, _ := reg.GetSensor("good")
	assert.Equal(t, 45000.0, good.GetMovingAvg())
	reading, ok := good.GetLastReading()
	assert.True(t, ok)
	assert.Equal(t, 45000.0, reading.Value)

	// ... and the other one has no reading, so it is stale until its first successful reading
	bad, _ := reg.GetSensor("bad")
	_, ok = bad.GetLastReading()
	assert.False(t, ok)
	_, err = sensors.FreshReading(bad, time.Now(), time.Hour)
	assert.ErrorIs(t, err, sensors.ErrSensorStale)

	// once it can be read, the first reading replaces the average (no slow rise from 0)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "missing"), []byte("70000"), 0o644))
	updateSensor(bad, util.NewFakeClock(time.Unix(1000, 0)))
	assert.Equal(t, 70000.0, bad.GetMovingAvg())
}
