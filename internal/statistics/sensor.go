package statistics

import (
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/prometheus/client_golang/prometheus"
)

const subsystemSensor = "sensor"

type SensorCollector struct {
	sensors []sensors.Sensor
	value   *prometheus.Desc
}

func NewSensorCollector(sensors []sensors.Sensor) *SensorCollector {
	return &SensorCollector{
		sensors: sensors,
		value: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystemSensor, "value"),
			"Current value of the sensor (moving average)",
			[]string{"id"}, nil,
		),
	}
}

func (collector *SensorCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.value
}

// Collect implements required collect function for all prometheus collectors
func (collector *SensorCollector) Collect(ch chan<- prometheus.Metric) {
	for _, sensor := range collector.sensors {
		sensorId := sensor.GetId()
		// the moving average maintained by the sensor monitor, which is also what the curves use.
		// Reading the sensor here would run cmd scripts, SMART ioctls or NVML calls on every scrape.
		value := sensor.GetMovingAvg()
		ch <- prometheus.MustNewConstMetric(collector.value, prometheus.GaugeValue, value, sensorId)
	}
}
