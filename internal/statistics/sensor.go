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
			"Current value of the sensor",
			[]string{"id"}, nil,
		),
	}
}

func (collector *SensorCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.value
}

//Collect implements required collect function for all promehteus collectors
func (collector *SensorCollector) Collect(ch chan<- prometheus.Metric) {
	for _, sensor := range collector.sensors {
		sensorId := sensor.GetId()
		value, _ := sensor.GetValue()
		ch <- prometheus.MustNewConstMetric(collector.value, prometheus.GaugeValue, value, sensorId)
	}
}
