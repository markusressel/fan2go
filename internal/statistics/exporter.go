package statistics

import (
	echoProm "github.com/labstack/echo-contrib/prometheus"
	"github.com/labstack/echo/v4"
	"github.com/markusressel/fan2go/internal/api"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	namespace = "fan2go"
)

func Register(collector prometheus.Collector) {
	prometheus.MustRegister(collector)
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
