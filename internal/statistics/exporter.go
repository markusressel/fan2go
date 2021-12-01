package statistics

import "github.com/prometheus/client_golang/prometheus"

const (
	namespace = "fan2go"
)

func Register(collector prometheus.Collector) {
	prometheus.MustRegister(collector)
}
