package statistics

import (
	"strings"
	"testing"

	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFan only implements what the collectors may use. Any other method, e.g. a hardware read
// like GetPwm or GetRpm, panics because the embedded interface is nil.
type fakeFan struct {
	fans.Fan
	id          string
	supportsRpm bool
}

func (f *fakeFan) GetId() string { return f.id }
func (f *fakeFan) Supports(feature fans.FeatureFlag) bool {
	return feature == fans.FeatureRpmSensor && f.supportsRpm
}

// fakeFanController only implements what the fan collector may use
type fakeFanController struct {
	controller.FanController
	lastSetPwm *int
	rpmAvg     *float64
}

func (c *fakeFanController) GetRpmAvg() (float64, bool) {
	if c.rpmAvg == nil {
		return 0, false
	}
	return *c.rpmAvg, true
}

func (c *fakeFanController) GetLastSetPwm() (int, bool) {
	if c.lastSetPwm == nil {
		return 0, false
	}
	return *c.lastSetPwm, true
}

// fakeSensor only implements what the sensor collector may use. GetValue (a hardware read) panics.
type fakeSensor struct {
	sensors.Sensor
	id        string
	movingAvg float64
}

func (s *fakeSensor) GetId() string         { return s.id }
func (s *fakeSensor) GetMovingAvg() float64 { return s.movingAvg }

func TestFanCollector_UsesControllerStateWithoutHardwareReads(t *testing.T) {
	// GIVEN
	pwm := 120
	rpmAvg := 1234.5
	collector := NewFanCollector(map[fans.Fan]controller.FanController{
		&fakeFan{id: "with_rpm", supportsRpm: true}: &fakeFanController{lastSetPwm: &pwm, rpmAvg: &rpmAvg},
		// no pwm set by the controller yet, and no rpm sensor
		&fakeFan{id: "unknown_pwm"}: &fakeFanController{rpmAvg: &rpmAvg},
		// rpm sensor, but the controller's RPM monitor hasn't seeded the average yet
		&fakeFan{id: "unseeded_rpm", supportsRpm: true}: &fakeFanController{},
	})

	// WHEN / THEN
	expected := `
# HELP fan2go_fan_pwm PWM value last set to the fan by fan2go (raw value, after applying the pwmMap)
# TYPE fan2go_fan_pwm gauge
fan2go_fan_pwm{id="with_rpm"} 120
# HELP fan2go_fan_rpm Current RPM value of the fan (moving average)
# TYPE fan2go_fan_rpm gauge
fan2go_fan_rpm{id="with_rpm"} 1234.5
`
	assert.NoError(t, testutil.CollectAndCompare(collector, strings.NewReader(expected)))
}

func TestSensorCollector_UsesMovingAverageWithoutHardwareReads(t *testing.T) {
	// GIVEN
	collector := NewSensorCollector([]sensors.Sensor{
		&fakeSensor{id: "cpu", movingAvg: 45250},
	})

	// WHEN / THEN
	expected := `
# HELP fan2go_sensor_value Current value of the sensor (moving average)
# TYPE fan2go_sensor_value gauge
fan2go_sensor_value{id="cpu"} 45250
`
	assert.NoError(t, testutil.CollectAndCompare(collector, strings.NewReader(expected)))
}

func TestControllerCollector_DescribesAllMetrics(t *testing.T) {
	// GIVEN a pedantic registry, which fails for metrics that were not described
	fanController := controller.NewFanController(nil, &fakeFan{id: "fan"}, nil, nil, 0, false)
	registry := prometheus.NewPedanticRegistry()
	require.NoError(t, registry.Register(NewControllerCollector([]controller.FanController{fanController})))

	// WHEN
	families, err := registry.Gather()

	// THEN
	require.NoError(t, err)
	var names []string
	for _, family := range families {
		names = append(names, family.GetName())
	}
	assert.ElementsMatch(t, []string{
		"fan2go_controller_unexpected_pwm_value_count",
		"fan2go_controller_increased_minPwm_count",
		"fan2go_controller_minPwm_offset",
	}, names)
}

func TestCollectors_PassPedanticRegistry(t *testing.T) {
	pwm := 120
	rpmAvg := 1000.0
	collectors := map[string]prometheus.Collector{
		"fan": NewFanCollector(map[fans.Fan]controller.FanController{
			&fakeFan{id: "fan", supportsRpm: true}: &fakeFanController{lastSetPwm: &pwm, rpmAvg: &rpmAvg},
		}),
		"sensor":     NewSensorCollector([]sensors.Sensor{&fakeSensor{id: "sensor", movingAvg: 40000}}),
		"controller": NewControllerCollector([]controller.FanController{controller.NewFanController(nil, &fakeFan{id: "fan"}, nil, nil, 0, false)}),
	}
	for name, collector := range collectors {
		t.Run(name, func(t *testing.T) {
			registry := prometheus.NewPedanticRegistry()
			require.NoError(t, registry.Register(collector))
			_, err := registry.Gather()
			assert.NoError(t, err)
		})
	}
}
