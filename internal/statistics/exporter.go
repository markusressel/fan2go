package statistics

import (
	"fmt"
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

// ReplaceAll replaces the application-specific collectors registered with Prometheus by the given ones,
// e.g. after a config reload. If one of the new collectors can't be registered, the previous collectors
// are registered again, and the error is returned.
func ReplaceAll(collectors []prometheus.Collector) error {
	mu.Lock()
	defer mu.Unlock()

	previous := activeCollectors
	unregister(previous)

	if err := register(collectors); err != nil {
		if restoreErr := register(previous); restoreErr != nil {
			activeCollectors = nil
			return fmt.Errorf("%w (restoring the previous collectors failed too: %w)", err, restoreErr)
		}
		// activeCollectors are still the previous collectors
		return err
	}
	activeCollectors = collectors
	return nil
}

// UnregisterAll removes the application-specific collectors from Prometheus (e.g. in tests).
func UnregisterAll() {
	mu.Lock()
	defer mu.Unlock()

	unregister(activeCollectors)
	activeCollectors = nil
}

// register registers all collectors with the default Prometheus registry. If one of them can't be registered,
// the ones registered before it are unregistered again, and the error is returned.
func register(collectors []prometheus.Collector) error {
	for i, c := range collectors {
		if err := prometheus.Register(c); err != nil {
			unregister(collectors[:i])
			return fmt.Errorf("error registering statistics collector: %w", err)
		}
	}
	return nil
}

func unregister(collectors []prometheus.Collector) {
	for _, c := range collectors {
		prometheus.Unregister(c)
	}
}

// CreateStatisticsService creates the webserver that serves the Prometheus metrics at /metrics
func CreateStatisticsService() *echo.Echo {
	echoPrometheus := echo.New()
	echoPrometheus.GET("/metrics", echo.WrapHandler(promhttp.Handler()))
	return echoPrometheus
}
