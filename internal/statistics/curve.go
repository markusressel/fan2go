package statistics

import (
	"errors"

	"github.com/markusressel/fan2go/internal/curves"
	"github.com/prometheus/client_golang/prometheus"
)

const subsystemCurve = "curve"

type CurveCollector struct {
	curves []curves.SpeedCurve
	value  *prometheus.Desc
	err    *prometheus.Desc
}

func NewCurveCollector(curves []curves.SpeedCurve) *CurveCollector {
	return &CurveCollector{
		curves: curves,
		value: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystemCurve, "value"),
			"Current value of the curve, not exported while the last evaluation failed (see fan2go_curve_error) or before the first one",
			[]string{"id"}, nil,
		),
		err: prometheus.NewDesc(prometheus.BuildFQName(namespace, subsystemCurve, "error"),
			"1 if the last evaluation of the curve failed (e.g. a sensor is stale), so the fans using it apply their onSensorFailure action; 0 otherwise. Not exported before the first evaluation",
			[]string{"id"}, nil,
		),
	}
}

func (collector *CurveCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.value
	ch <- collector.err
}

// Collect implements required collect function for all prometheus collectors
func (collector *CurveCollector) Collect(ch chan<- prometheus.Metric) {
	for _, curve := range collector.curves {
		curveId := curve.GetId()
		// the result of the last cycle of the curve evaluator, the curve isn't evaluated here
		result := curve.CurrentResult()
		if errors.Is(result.Err, curves.ErrCurveNotEvaluated) {
			continue
		}
		if result.Err != nil {
			// the value is the last successful one, which doesn't control the fans anymore
			ch <- prometheus.MustNewConstMetric(collector.err, prometheus.GaugeValue, 1, curveId)
			continue
		}
		ch <- prometheus.MustNewConstMetric(collector.err, prometheus.GaugeValue, 0, curveId)
		ch <- prometheus.MustNewConstMetric(collector.value, prometheus.GaugeValue, result.Value, curveId)
	}
}
