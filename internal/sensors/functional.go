package sensors

import (
	"fmt"
	"sync"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

// FunctionSensor is a sensor that aggregates values from other sensors
type FunctionSensor struct {
	Config configuration.SensorConfig

	mu        sync.Mutex
	movingAvg float64
}

// GetId returns the unique identifier of this sensor
func (s *FunctionSensor) GetId() string {
	return s.Config.ID
}

// GetLabel returns a human-readable label for this sensor
func (s *FunctionSensor) GetLabel() string {
	return fmt.Sprintf("Function (%s)", s.Config.Function.Type)
}

// GetConfig returns the configuration of this sensor
func (s *FunctionSensor) GetConfig() configuration.SensorConfig {
	return s.Config
}

// GetValue returns the current aggregated value of this sensor
func (s *FunctionSensor) GetValue() (float64, error) {
	var values []float64
	for _, sensorId := range s.Config.Function.Sensors {
		sensor, ok := GetSensor(sensorId)
		if !ok {
			return 0, fmt.Errorf("sensor '%s' not found", sensorId)
		}
		val, err := sensor.GetValue()
		if err != nil {
			return 0, fmt.Errorf("failed to get value from sensor '%s': %w", sensorId, err)
		}
		values = append(values, val)
	}

	var value float64
	switch s.Config.Function.Type {
	case configuration.FunctionSum:
		value = util.Sum(values)
	case configuration.FunctionDifference:
		value = util.Difference(values)
	case configuration.FunctionDelta:
		value = util.Delta(values)
	case configuration.FunctionMinimum:
		value = util.MinValOrElse(values, values[0])
	case configuration.FunctionMaximum:
		value = util.MaxValOrElse(values, values[0])
	case configuration.FunctionAverage:
		value = util.Avg(values)
	default:
		return 0, fmt.Errorf("unknown sensor function type: %s", s.Config.Function.Type)
	}

	ui.Debug("Evaluating sensor function '%s'. Sensor values: '%v' Aggregated value: %.2f", s.Config.ID, values, value)
	return value, nil
}

// GetMovingAvg returns the moving average of this sensor's value
func (s *FunctionSensor) GetMovingAvg() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.movingAvg
}

// SetMovingAvg sets the moving average of this sensor's value
func (s *FunctionSensor) SetMovingAvg(avg float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.movingAvg = avg
}
