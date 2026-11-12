package internal

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/stretchr/testify/assert"
)

// panickingSensor panics on the first panicCount reads, and returns value afterward.
// Every read is announced on reads.
type panickingSensor struct {
	sensors.ReadingTracker

	panicCount int
	value      float64
	reads      chan struct{}

	mu        sync.Mutex
	readCount int
	movingAvg float64
}

func (s *panickingSensor) GetId() string    { return "panicking" }
func (s *panickingSensor) GetLabel() string { return "panicking" }
func (s *panickingSensor) GetConfig() configuration.SensorConfig {
	return configuration.SensorConfig{ID: "panicking"}
}

func (s *panickingSensor) GetValue() (float64, error) {
	s.mu.Lock()
	s.readCount++
	shouldPanic := s.readCount <= s.panicCount
	s.mu.Unlock()
	if s.reads != nil {
		s.reads <- struct{}{}
	}
	if shouldPanic {
		panic("sensor backend exploded")
	}
	return s.value, nil
}

func (s *panickingSensor) GetMovingAvg() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.movingAvg
}

func (s *panickingSensor) SetMovingAvg(avg float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.movingAvg = avg
}

func TestSensorMonitor_PanicWhileUpdatingSensor_IsLoggedAndMonitorKeepsRunning(t *testing.T) {
	originalConfig := configuration.CurrentConfig
	defer func() {
		configuration.CurrentConfig = originalConfig
	}()
	configuration.CurrentConfig.TempRollingWindowSize = 1

	// GIVEN: a sensor whose backend panics on the first two reads
	sensor := &panickingSensor{panicCount: 2, value: 42, reads: make(chan struct{})}
	monitor := NewSensorMonitor(sensor, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	// WHEN
	go func() { done <- monitor.Run(ctx) }()
	for i := 0; i < 3; i++ {
		select {
		case <-sensor.reads:
		case <-time.After(5 * time.Second):
			t.Fatalf("sensor monitor stopped reading the sensor after %d reads", i)
		}
	}
	cancel()
	// let a read that started before the cancellation finish
	go func() {
		for range sensor.reads {
		}
	}()

	// THEN: the monitor kept running after the panics and used the third value
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("sensor monitor did not stop")
	}
	close(sensor.reads)
	assert.Equal(t, 42.0, sensor.GetMovingAvg())
}
