package statistics

import (
	"sync"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	namespace = "fan2go"
)

var (
	mu               sync.Mutex
	activeCollectors []prometheus.Collector
)

// Register adds the collector to Prometheus and tracks it for future cleanup.
func Register(c prometheus.Collector) {
	mu.Lock()
	defer mu.Unlock()

	// Register with the global Prometheus registry
	prometheus.MustRegister(c)

	// Track it locally so we can clean it up later
	activeCollectors = append(activeCollectors, c)
}

// UnregisterAll safely removes only the application-specific metrics
// from Prometheus, preventing duplicate panics during a SIGHUP reload.
func UnregisterAll() {
	mu.Lock()
	defer mu.Unlock()

	for _, c := range activeCollectors {
		prometheus.Unregister(c)
	}

	// Clear the slice so it's fresh for the next initialization
	activeCollectors = nil
}

// CreateStatisticsService creates the webserver that serves the Prometheus metrics at /metrics
func CreateStatisticsService() *echo.Echo {
	echoPrometheus := echo.New()
	echoPrometheus.GET("/metrics", echo.WrapHandler(promhttp.Handler()))
	return echoPrometheus
}
