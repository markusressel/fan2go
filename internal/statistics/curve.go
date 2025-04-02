package statistics

import (
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/prometheus/client_golang/prometheus"
)

const subsystemCurve = "curve"

type CurveCollector struct {
	curves []curves.SpeedCurve
	value  *prometheus.Desc
}

func NewCurveCollector(curves []curves.SpeedCurve) *CurveCollector {
	return &CurveCollector{
		curves: curves,
		value: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystemCurve, "value"),
			"Current value of the curve",
			[]string{"id"}, nil,
		),
	}
}

func (collector *CurveCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.value
}

// Collect implements required collect function for all prometheus collectors
func (collector *CurveCollector) Collect(ch chan<- prometheus.Metric) {
	for _, curve := range collector.curves {
		curveId := curve.GetId()
		value := curve.CurrentValue()
		ch <- prometheus.MustNewConstMetric(collector.value, prometheus.GaugeValue, float64(value), curveId)
	}
}
