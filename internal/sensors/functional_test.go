package sensors

import (
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
)

func TestFunctionSensor_GetValue(t *testing.T) {
	s1 := &VirtualSensor{Name: "s1", Value: 10}
	s2 := &VirtualSensor{Name: "s2", Value: 20}
	s3 := &VirtualSensor{Name: "s3", Value: 30}
	RegisterSensor(s1)
	RegisterSensor(s2)
	RegisterSensor(s3)

	tests := []struct {
		name     string
		funcType string
		sensors  []string
		expected float64
	}{
		{"Average-2", configuration.FunctionAverage, []string{"s1", "s2"}, 15},
		{"Average-3", configuration.FunctionAverage, []string{"s1", "s2", "s3"}, 20},
		{"Sum-2", configuration.FunctionSum, []string{"s1", "s2"}, 30},
		{"Sum-3", configuration.FunctionSum, []string{"s1", "s2", "s3"}, 60},
		{"Min-3", configuration.FunctionMinimum, []string{"s1", "s2", "s3"}, 10},
		{"Max-3", configuration.FunctionMaximum, []string{"s1", "s2", "s3"}, 30},
		{"Delta-3", configuration.FunctionDelta, []string{"s1", "s2", "s3"}, 20},
		{"Difference-3", configuration.FunctionDifference, []string{"s3", "s1", "s2"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := &FunctionSensor{
				Config: configuration.SensorConfig{
					ID: "fs",
					Function: &configuration.FunctionSensorConfig{
						Type:    tt.funcType,
						Sensors: tt.sensors,
					},
				},
			}

			val, err := fs.GetValue()
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, val)
		})
	}
}

func TestFunctionSensor_GetValue_Error(t *testing.T) {
	fs := &FunctionSensor{
		Config: configuration.SensorConfig{
			ID: "fs",
			Function: &configuration.FunctionSensorConfig{
				Type:    configuration.FunctionAverage,
				Sensors: []string{"non-existent"},
			},
		},
	}

	val, err := fs.GetValue()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sensor 'non-existent' not found")
	assert.Equal(t, 0.0, val)
}

func TestFunctionSensor_GetMovingAvg(t *testing.T) {
	fs := &FunctionSensor{}
	fs.SetMovingAvg(42.5)
	assert.Equal(t, 42.5, fs.GetMovingAvg())
}

func TestFunctionSensor_Metadata(t *testing.T) {
	config := configuration.SensorConfig{
		ID: "fs",
		Function: &configuration.FunctionSensorConfig{
			Type:    configuration.FunctionAverage,
			Sensors: []string{"s1", "s2"},
		},
	}
	fs := &FunctionSensor{
		Config: config,
	}

	assert.Equal(t, "fs", fs.GetId())
	assert.Equal(t, "Function (average)", fs.GetLabel())
	assert.Equal(t, config, fs.GetConfig())
}
