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

	// Singleton variables for Echo HTTP metrics
	httpMetricsOnce sync.Once
	httpMetricsProm *echoProm.Prometheus
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

	// Create Prometheus server
	echoPrometheus := echo.New()
	echoPrometheus.HideBanner = true

	// Initialize the Prometheus HTTP middleware exactly ONCE for the daemon's lifecycle
	httpMetricsOnce.Do(func() {
		httpMetricsProm = echoProm.NewPrometheus("echo", nil)
	})

	// Scrape metrics from Main Server using the singleton middleware
	parentServer.Use(httpMetricsProm.HandlerFunc)

	// Setup metrics endpoint at the dedicated statistics server
	httpMetricsProm.SetMetricsPath(echoPrometheus)

	return echoPrometheus
}
