package sensors

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrSensorStale is returned (wrapped) when the last successful reading of a sensor is older than
// the sensor stale timeout, or when the sensor has not been read successfully yet.
var ErrSensorStale = errors.New("sensor value is stale")

// Reading is a successful raw reading of a sensor.
type Reading struct {
	// Value is the raw sensor value (e.g. millidegrees Celsius), without any smoothing
	Value float64
	// Time is when the value was read
	Time time.Time
}

// ReadingTracker records the successful and failed readings of a sensor.
// It is written by the sensor monitor and read by curves, so it is safe for concurrent use.
// Embed it in a Sensor implementation to implement the corresponding methods of the Sensor interface.
type ReadingTracker struct {
	mu                  sync.Mutex
	last                Reading
	hasReading          bool
	consecutiveFailures int
	totalFailures       int
}

// ReadingStatus is a consistent snapshot of the readings recorded by a ReadingTracker.
type ReadingStatus struct {
	// LastReading is the last successful reading, only valid if HasReading is true
	LastReading Reading
	// HasReading is false until the first successful reading
	HasReading bool
	// ConsecutiveFailures is the number of failed readings since the last successful one
	ConsecutiveFailures int
	// TotalFailures is the number of failed readings since the sensor was created
	TotalFailures int
}

// IsStale returns true if the curves don't accept the value of the sensor at now (see FreshReading):
// it has no successful reading yet, or its last successful reading is older than timeout.
func (s ReadingStatus) IsStale(now time.Time, timeout time.Duration) bool {
	return !s.HasReading || now.Sub(s.LastReading.Time) > timeout
}

// RecordReading stores a successful reading and resets the failure counter.
// It returns the number of consecutive failed readings before this one.
func (t *ReadingTracker) RecordReading(r Reading) (previousFailures int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	previousFailures = t.consecutiveFailures
	t.last = r
	t.hasReading = true
	t.consecutiveFailures = 0
	return previousFailures
}

// RecordFailure counts a failed reading and returns the number of consecutive failed readings,
// including this one. The last successful reading is kept.
func (t *ReadingTracker) RecordFailure() (consecutiveFailures int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.consecutiveFailures++
	t.totalFailures++
	return t.consecutiveFailures
}

// GetReadingStatus returns a consistent snapshot of the recorded readings and failures.
func (t *ReadingTracker) GetReadingStatus() ReadingStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return ReadingStatus{
		LastReading:         t.last,
		HasReading:          t.hasReading,
		ConsecutiveFailures: t.consecutiveFailures,
		TotalFailures:       t.totalFailures,
	}
}

// GetLastReading returns the last successful reading, and false if there is none yet.
func (t *ReadingTracker) GetLastReading() (Reading, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last, t.hasReading
}

// GetConsecutiveFailures returns the number of failed readings since the last successful one.
func (t *ReadingTracker) GetConsecutiveFailures() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.consecutiveFailures
}

// FreshReading returns the last successful reading of the sensor, if it is not older than timeout at now.
// Otherwise, it returns an error that wraps ErrSensorStale.
func FreshReading(s Sensor, now time.Time, timeout time.Duration) (Reading, error) {
	reading, ok := s.GetLastReading()
	if !ok {
		return Reading{}, fmt.Errorf("sensor %s: %w: no successful reading yet", s.GetId(), ErrSensorStale)
	}
	age := now.Sub(reading.Time)
	if age > timeout {
		return reading, fmt.Errorf("sensor %s: %w: last successful reading was %s ago (sensorStaleTimeout: %s)",
			s.GetId(), ErrSensorStale, age.Round(time.Millisecond), timeout)
	}
	return reading, nil
}
