package curves

import (
	"github.com/markusressel/fan2go/internal/sensors"
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

func (r *MockRegistry) RegisterCurve(c SpeedCurve) {
	r.curves[c.GetId()] = c
	if binder, ok := c.(interface{ BindRegistry(RegistryReader) }); ok {
		binder.BindRegistry(r)
	}
}
