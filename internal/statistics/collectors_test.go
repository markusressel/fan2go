package statistics

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/controller/status"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
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
	fanId      string
	state      status.State
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

func (c *fakeFanController) GetFanId() string { return c.fanId }

func (c *fakeFanController) GetStatus() status.Status {
	return status.Status{FanId: c.fanId, State: c.state}
}

// fakeSensor only implements what the sensor collector may use. GetValue (a hardware read) panics.
type fakeSensor struct {
	sensors.Sensor
	id        string
	movingAvg float64
	status    sensors.ReadingStatus
}

func (s *fakeSensor) GetId() string                           { return s.id }
func (s *fakeSensor) GetMovingAvg() float64                   { return s.movingAvg }
func (s *fakeSensor) GetReadingStatus() sensors.ReadingStatus { return s.status }

// fakeCurve only implements what the curve collector may use. Evaluate panics.
type fakeCurve struct {
	curves.SpeedCurve
	id     string
	result curves.CurveResult
}

func (c *fakeCurve) GetId() string                     { return c.id }
func (c *fakeCurve) CurrentResult() curves.CurveResult { return c.result }

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
	now := time.Unix(1700000000, 0)
	clock := util.NewFakeClock(now)
	configuration.CurrentConfig.SensorStaleTimeout = nil
	staleTimeout := configuration.CurrentConfig.GetSensorStaleTimeout()
	collector := NewSensorCollector([]sensors.Sensor{
		&fakeSensor{id: "fresh", movingAvg: 45250, status: sensors.ReadingStatus{
			LastReading: sensors.Reading{Value: 45000, Time: now.Add(-time.Second)}, HasReading: true,
		}},
		// the last reading is too old, and the sensor failed since then
		&fakeSensor{id: "stale", movingAvg: 50000, status: sensors.ReadingStatus{
			LastReading: sensors.Reading{Value: 50000, Time: now.Add(-staleTimeout - time.Millisecond)}, HasReading: true,
			ConsecutiveFailures: 3, TotalFailures: 5,
		}},
		// never read successfully: no value (instead of 0), and no timestamp
		&fakeSensor{id: "never_read", status: sensors.ReadingStatus{ConsecutiveFailures: 2, TotalFailures: 2}},
	})
	collector.clock = clock

	// WHEN / THEN
	expected := fmt.Sprintf(`
# HELP fan2go_sensor_value Current value of the sensor (moving average), not exported until the first successful reading
# TYPE fan2go_sensor_value gauge
fan2go_sensor_value{id="fresh"} 45250
fan2go_sensor_value{id="stale"} 50000
# HELP fan2go_sensor_stale 1 if the value of the sensor is stale (no successful reading yet, or the last one is older than sensorStaleTimeout), so the curves using it fail; 0 otherwise
# TYPE fan2go_sensor_stale gauge
fan2go_sensor_stale{id="fresh"} 0
fan2go_sensor_stale{id="never_read"} 1
fan2go_sensor_stale{id="stale"} 1
# HELP fan2go_sensor_last_reading_timestamp_seconds Unix time of the last successful reading of the sensor, not exported until the first one
# TYPE fan2go_sensor_last_reading_timestamp_seconds gauge
fan2go_sensor_last_reading_timestamp_seconds{id="fresh"} 1.699999999e+09
fan2go_sensor_last_reading_timestamp_seconds{id="stale"} %v
# HELP fan2go_sensor_read_failures_total Number of failed readings of the sensor since it was created (start or config reload)
# TYPE fan2go_sensor_read_failures_total counter
fan2go_sensor_read_failures_total{id="fresh"} 0
fan2go_sensor_read_failures_total{id="never_read"} 2
fan2go_sensor_read_failures_total{id="stale"} 5
`, float64(now.Add(-staleTimeout-time.Millisecond).UnixNano())/1e9)
	assert.NoError(t, testutil.CollectAndCompare(collector, strings.NewReader(expected)))
}

func TestSensorCollector_StaleExactlyAtTimeout(t *testing.T) {
	// GIVEN: a reading exactly sensorStaleTimeout old, which the curves still accept
	now := time.Unix(1700000000, 0)
	configuration.CurrentConfig.SensorStaleTimeout = nil
	staleTimeout := configuration.CurrentConfig.GetSensorStaleTimeout()
	collector := NewSensorCollector([]sensors.Sensor{
		&fakeSensor{id: "s", status: sensors.ReadingStatus{LastReading: sensors.Reading{Time: now.Add(-staleTimeout)}, HasReading: true}},
	})
	collector.clock = util.NewFakeClock(now)

	// WHEN / THEN
	expected := `
# HELP fan2go_sensor_stale 1 if the value of the sensor is stale (no successful reading yet, or the last one is older than sensorStaleTimeout), so the curves using it fail; 0 otherwise
# TYPE fan2go_sensor_stale gauge
fan2go_sensor_stale{id="s"} 0
`
	assert.NoError(t, testutil.CollectAndCompare(collector, strings.NewReader(expected), "fan2go_sensor_stale"))
}

func TestCurveCollector_OmitsValueWhileTheCurveFails(t *testing.T) {
	// GIVEN
	at := time.Unix(1700000000, 0)
	collector := NewCurveCollector([]curves.SpeedCurve{
		&fakeCurve{id: "ok", result: curves.CurveResult{Value: 128, EvaluatedAt: at}},
		// the value is the last successful one, which doesn't control the fans anymore
		&fakeCurve{id: "failing", result: curves.CurveResult{Value: 80, Err: sensors.ErrSensorStale, EvaluatedAt: at}},
		&fakeCurve{id: "not_evaluated", result: curves.CurveResult{Err: curves.ErrCurveNotEvaluated}},
	})

	// WHEN / THEN
	expected := `
# HELP fan2go_curve_value Current value of the curve, not exported while the last evaluation failed (see fan2go_curve_error) or before the first one
# TYPE fan2go_curve_value gauge
fan2go_curve_value{id="ok"} 128
# HELP fan2go_curve_error 1 if the last evaluation of the curve failed (e.g. a sensor is stale), so the fans using it apply their onSensorFailure action; 0 otherwise. Not exported before the first evaluation
# TYPE fan2go_curve_error gauge
fan2go_curve_error{id="failing"} 1
fan2go_curve_error{id="ok"} 0
`
	assert.NoError(t, testutil.CollectAndCompare(collector, strings.NewReader(expected)))
}

func TestControllerCollector_ExportsTheStateAsEnum(t *testing.T) {
	// GIVEN
	collector := NewControllerCollector([]controller.FanController{
		&fakeFanController{fanId: "a", state: status.StateSensorFailureRestored},
	})

	// WHEN / THEN: exactly the current state is 1
	expected := `
# HELP fan2go_controller_state State of the fan controller: 1 for the current state, 0 for all others (stopped, initializing, running, sensor_failure_max, sensor_failure_restored, stopped_pwm_write_failed, stopped_stalled, stopped_error)
# TYPE fan2go_controller_state gauge
fan2go_controller_state{id="a",state="initializing"} 0
fan2go_controller_state{id="a",state="running"} 0
fan2go_controller_state{id="a",state="sensor_failure_max"} 0
fan2go_controller_state{id="a",state="sensor_failure_restored"} 1
fan2go_controller_state{id="a",state="stopped"} 0
fan2go_controller_state{id="a",state="stopped_error"} 0
fan2go_controller_state{id="a",state="stopped_pwm_write_failed"} 0
fan2go_controller_state{id="a",state="stopped_stalled"} 0
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
		"fan2go_controller_stall_count",
		"fan2go_controller_kick_start_count",
		"fan2go_controller_failed_kick_start_count",
		"fan2go_controller_kick_start_pwm",
		"fan2go_controller_state",
	}, names)
}

func TestCollectors_PassPedanticRegistry(t *testing.T) {
	pwm := 120
	rpmAvg := 1000.0
	collectors := map[string]prometheus.Collector{
		"fan": NewFanCollector(map[fans.Fan]controller.FanController{
			&fakeFan{id: "fan", supportsRpm: true}: &fakeFanController{lastSetPwm: &pwm, rpmAvg: &rpmAvg},
		}),
		"sensor": NewSensorCollector([]sensors.Sensor{&fakeSensor{id: "sensor", movingAvg: 40000, status: sensors.ReadingStatus{
			LastReading: sensors.Reading{Time: time.Now()}, HasReading: true, TotalFailures: 1,
		}}}),
		"curve": NewCurveCollector([]curves.SpeedCurve{
			&fakeCurve{id: "ok", result: curves.CurveResult{Value: 1}},
			&fakeCurve{id: "failing", result: curves.CurveResult{Err: sensors.ErrSensorStale}},
		}),
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
