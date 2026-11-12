package sensors

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileSensor_MissingFile_ReturnsError(t *testing.T) {
	// GIVEN: a file sensor whose file doesn't exist
	sensor, err := NewSensor(configuration.SensorConfig{
		ID:   "file",
		File: &configuration.FileSensorConfig{Path: filepath.Join(t.TempDir(), "missing")},
	})
	require.NoError(t, err)

	// WHEN
	_, err = sensor.GetValue()

	// THEN: the failed read is reported, not turned into a reading of 0
	assert.Error(t, err)
}

func TestFileSensor_ExistingFile_ReturnsValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "temp")
	require.NoError(t, os.WriteFile(path, []byte("42000\n"), 0o644))
	sensor, err := NewSensor(configuration.SensorConfig{
		ID:   "file",
		File: &configuration.FileSensorConfig{Path: path},
	})
	require.NoError(t, err)

	value, err := sensor.GetValue()

	assert.NoError(t, err)
	assert.Equal(t, 42000.0, value)
}

func TestReadingTracker_RecordsReadingsAndFailures(t *testing.T) {
	tracker := &ReadingTracker{}
	_, ok := tracker.GetLastReading()
	assert.False(t, ok)

	at := time.Unix(1000, 0)
	assert.Equal(t, 0, tracker.RecordReading(Reading{Value: 40000, Time: at}))
	assert.Equal(t, 1, tracker.RecordFailure())
	assert.Equal(t, 2, tracker.RecordFailure())
	assert.Equal(t, 2, tracker.GetConsecutiveFailures())

	// a failure keeps the last successful reading
	reading, ok := tracker.GetLastReading()
	assert.True(t, ok)
	assert.Equal(t, Reading{Value: 40000, Time: at}, reading)

	// a successful reading resets the failure counter and returns the previous failures
	assert.Equal(t, 2, tracker.RecordReading(Reading{Value: 41000, Time: at.Add(time.Second)}))
	assert.Equal(t, 0, tracker.GetConsecutiveFailures())
}

func TestFreshReading(t *testing.T) {
	sensor := &HwmonSensor{Config: configuration.SensorConfig{ID: "s"}}
	start := time.Unix(1000, 0)
	timeout := 10 * time.Second

	// no successful reading yet: stale from the start
	_, err := FreshReading(sensor, start, timeout)
	assert.True(t, errors.Is(err, ErrSensorStale))

	sensor.RecordReading(Reading{Value: 50000, Time: start})

	reading, err := FreshReading(sensor, start.Add(timeout), timeout)
	assert.NoError(t, err)
	assert.Equal(t, 50000.0, reading.Value)

	_, err = FreshReading(sensor, start.Add(timeout+time.Millisecond), timeout)
	assert.True(t, errors.Is(err, ErrSensorStale))
	assert.Contains(t, err.Error(), "sensor s")
}

// The sensor monitor writes the reading state while curves read it.
func TestReadingTracker_ConcurrentAccess(t *testing.T) {
	sensor := &CmdSensor{Config: configuration.SensorConfig{ID: "s"}}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			if i%2 == 0 {
				sensor.RecordReading(Reading{Value: float64(i), Time: time.Unix(int64(i), 0)})
			} else {
				sensor.RecordFailure()
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_, _ = FreshReading(sensor, time.Unix(int64(i), 0), time.Second)
			_ = sensor.GetConsecutiveFailures()
		}
	}()
	wg.Wait()
}
