package registry

import (
	cmap "github.com/orcaman/concurrent-map/v2"
	"github.com/qdm12/reprint"

	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
)

type Registry struct {
	fans    cmap.ConcurrentMap[string, fans.Fan]
	sensors cmap.ConcurrentMap[string, sensors.Sensor]
	curves  cmap.ConcurrentMap[string, curves.SpeedCurve]
}

func NewRegistry() *Registry {
	return &Registry{
		fans:    cmap.New[fans.Fan](),
		sensors: cmap.New[sensors.Sensor](),
		curves:  cmap.New[curves.SpeedCurve](),
	}
}

// Fans
func (r *Registry) RegisterFan(fan fans.Fan) {
	r.fans.Set(fan.GetId(), fan)
}

func (r *Registry) GetFan(id string) (fans.Fan, bool) {
	return r.fans.Get(id)
}

func (r *Registry) SnapshotFans() map[string]fans.Fan {
	if r.fans.IsEmpty() {
		return map[string]fans.Fan{}
	}
	return reprint.This(r.fans.Items()).(map[string]fans.Fan)
}

// Sensors
func (r *Registry) RegisterSensor(sensor sensors.Sensor) {
	r.sensors.Set(sensor.GetId(), sensor)
}

func (r *Registry) GetSensor(id string) (sensors.Sensor, bool) {
	return r.sensors.Get(id)
}

func (r *Registry) SnapshotSensors() map[string]sensors.Sensor {
	if r.sensors.IsEmpty() {
		return map[string]sensors.Sensor{}
	}
	return reprint.This(r.sensors.Items()).(map[string]sensors.Sensor)
}

// Curves
func (r *Registry) RegisterCurve(curve curves.SpeedCurve) {
	r.curves.Set(curve.GetId(), curve)
}

func (r *Registry) GetCurve(id string) (curves.SpeedCurve, bool) {
	return r.curves.Get(id)
}

func (r *Registry) SnapshotCurves() map[string]curves.SpeedCurve {
	if r.curves.IsEmpty() {
		return map[string]curves.SpeedCurve{}
	}
	return reprint.This(r.curves.Items()).(map[string]curves.SpeedCurve)
}
