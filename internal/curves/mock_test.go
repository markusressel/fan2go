package curves

import (
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/stretchr/testify/require"
)

type MockRegistry struct {
	sensors map[string]sensors.Sensor
	curves  map[string]SpeedCurve
}

func (r *MockRegistry) GetSensor(id string) (sensors.Sensor, bool) {
	s, ok := r.sensors[id]
	return s, ok
}

func (r *MockRegistry) GetCurve(id string) (SpeedCurve, bool) {
	c, ok := r.curves[id]
	return c, ok
}

func NewMockRegistry() *MockRegistry {
	return &MockRegistry{
		sensors: make(map[string]sensors.Sensor),
		curves:  make(map[string]SpeedCurve),
	}
}

func (r *MockRegistry) RegisterSensor(s sensors.Sensor) {
	r.sensors[s.GetId()] = s
}

// runEvaluatorCycle evaluates all curves of the registry once, in dependency order, like the daemon does
// every cycle, and returns the result of curve.
func runEvaluatorCycle(t *testing.T, reg *MockRegistry, curve SpeedCurve) (float64, error) {
	t.Helper()
	var all []SpeedCurve
	for _, c := range reg.curves {
		all = append(all, c)
	}
	evaluator, err := NewCurveEvaluator(all, time.Second)
	require.NoError(t, err)
	evaluator.RunCycle()
	result := curve.CurrentResult()
	return result.Value, result.Err
}

func (r *MockRegistry) RegisterCurve(c SpeedCurve) {
	r.curves[c.GetId()] = c
	if binder, ok := c.(interface{ BindRegistry(RegistryReader) }); ok {
		binder.BindRegistry(r)
	}
}
