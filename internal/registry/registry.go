package registry

import (
	cmap "github.com/orcaman/concurrent-map/v2"
	"github.com/qdm12/reprint"

	"github.com/markusressel/fan2go/internal/controller/status"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/warnings"
)

// FanController is the part of a fan controller (controller.FanController) the registry provides,
// so the registry doesn't depend on the controller package.
type FanController interface {
	GetFanId() string
	// GetStatus returns the current state of the controller, safe to call while it is running
	GetStatus() status.Status
}

type Registry struct {
	fans    cmap.ConcurrentMap[string, fans.Fan]
	sensors cmap.ConcurrentMap[string, sensors.Sensor]
	curves  cmap.ConcurrentMap[string, curves.SpeedCurve]
	// fan controllers by the id of the fan they control
	fanControllers cmap.ConcurrentMap[string, FanController]
	// warnings of the fans, sensors and curves of this registry
	warnings *warnings.Store
}

func NewRegistry() *Registry {
	return &Registry{
		fans:    cmap.New[fans.Fan](),
		sensors: cmap.New[sensors.Sensor](),
		curves:  cmap.New[curves.SpeedCurve](),

		fanControllers: cmap.New[FanController](),
		warnings:       warnings.NewStore(),
	}
}

// Warnings returns the warnings of the fans, sensors and curves of this registry. A new registry (e.g. after a
// config reload) starts without warnings.
func (r *Registry) Warnings() *warnings.Store {
	return r.warnings
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
	if binder, ok := curve.(interface{ BindRegistry(curves.RegistryReader) }); ok {
		binder.BindRegistry(r)
	}
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

// Fan controllers
func (r *Registry) RegisterFanController(fanController FanController) {
	r.fanControllers.Set(fanController.GetFanId(), fanController)
}

// GetFanController returns the controller of the fan with the given id.
func (r *Registry) GetFanController(fanId string) (FanController, bool) {
	return r.fanControllers.Get(fanId)
}

// SnapshotFanControllers returns all fan controllers by the id of the fan they control.
func (r *Registry) SnapshotFanControllers() map[string]FanController {
	return r.fanControllers.Items()
}
