package statistics

import (
	"sync"

	echoProm "github.com/labstack/echo-contrib/prometheus"
	"github.com/labstack/echo/v4"
	"github.com/markusressel/fan2go/internal/api"
	"github.com/prometheus/client_golang/prometheus"
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

func CreateStatisticsService() *echo.Echo {
	parentServer := api.CreateWebserver()

	// Create Prometheus server and Middleware
	echoPrometheus := echo.New()
	echoPrometheus.HideBanner = true
	prom := echoProm.NewPrometheus("echo", nil)

	// Scrape metrics from Main Server
	parentServer.Use(prom.HandlerFunc)
	// Setup metrics endpoint at another server
	prom.SetMetricsPath(echoPrometheus)
	return echoPrometheus
}
