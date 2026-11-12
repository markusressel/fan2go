package statistics

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/prometheus/client_golang/prometheus"
)

const subsystemSensor = "sensor"

type SensorCollector struct {
	sensors []sensors.Sensor
	// clock is the time source for the staleness of the sensor values (nil means util.RealClock)
	clock util.Clock

	value                *prometheus.Desc
	stale                *prometheus.Desc
	lastReadingTimestamp *prometheus.Desc
	readFailures         *prometheus.Desc
}

func NewSensorCollector(sensors []sensors.Sensor) *SensorCollector {
	return &SensorCollector{
		sensors: sensors,
		value: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystemSensor, "value"),
			"Current value of the sensor (moving average), not exported until the first successful reading",
			[]string{"id"}, nil,
		),
		stale: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystemSensor, "stale"),
			"1 if the value of the sensor is stale (no successful reading yet, or the last one is older than sensorStaleTimeout), so the curves using it fail; 0 otherwise",
			[]string{"id"}, nil,
		),
		lastReadingTimestamp: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystemSensor, "last_reading_timestamp_seconds"),
			"Unix time of the last successful reading of the sensor, not exported until the first one",
			[]string{"id"}, nil,
		),
		readFailures: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystemSensor, "read_failures_total"),
			"Number of failed readings of the sensor since it was created (start or config reload)",
			[]string{"id"}, nil,
		),
	}
}

func (collector *SensorCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.value
	ch <- collector.stale
	ch <- collector.lastReadingTimestamp
	ch <- collector.readFailures
}

// Collect implements required collect function for all prometheus collectors
func (collector *SensorCollector) Collect(ch chan<- prometheus.Metric) {
	clock := collector.clock
	if clock == nil {
		clock = util.RealClock
	}
	now := clock.Now()
	staleTimeout := configuration.CurrentConfig.GetSensorStaleTimeout()

	for _, sensor := range collector.sensors {
		sensorId := sensor.GetId()
		status := sensor.GetReadingStatus()

		if status.HasReading {
			// the moving average maintained by the sensor monitor, which is also what the curves use.
			// Reading the sensor here would run cmd scripts, SMART ioctls or NVML calls on every scrape.
			// Before the first successful reading, the average is meaningless (0), so it isn't exported.
			ch <- prometheus.MustNewConstMetric(collector.value, prometheus.GaugeValue, sensor.GetMovingAvg(), sensorId)
			ch <- prometheus.MustNewConstMetric(collector.lastReadingTimestamp, prometheus.GaugeValue,
				float64(status.LastReading.Time.UnixNano())/1e9, sensorId)
		}
		ch <- prometheus.MustNewConstMetric(collector.stale, prometheus.GaugeValue, boolToFloat(status.IsStale(now, staleTimeout)), sensorId)
		ch <- prometheus.MustNewConstMetric(collector.readFailures, prometheus.CounterValue, float64(status.TotalFailures), sensorId)
	}
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
