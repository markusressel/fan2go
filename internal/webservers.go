package internal

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"sync"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/markusressel/fan2go/internal/api"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/statistics"
	"github.com/markusressel/fan2go/internal/ui"
)

const webserverShutdownTimeout = 5 * time.Second

func startProfilingWebserver(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	go func() {
		ui.Info("Starting profiling webserver...")
		profilingConfig := configuration.CurrentConfig.Profiling
		address := fmt.Sprintf("%s:%d", profilingConfig.Host, profilingConfig.Port)
		ui.Error("Error running profiling webserver: %v", http.ListenAndServe(address, mux))
	}()

	<-ctx.Done()
	ui.Info("Stopping profiling webserver...")
	return nil
}

func startWebservers(ctx context.Context, reg *registry.Registry, wg *sync.WaitGroup) {
	if !configuration.CurrentConfig.Api.Enabled && !configuration.CurrentConfig.Statistics.Enabled {
		return
	}
	ui.Info("Starting Webservers...")

	if configuration.CurrentConfig.Api.Enabled {
		startRestServer(ctx, reg, wg)
	}

	if configuration.CurrentConfig.Statistics.Enabled {
		startStatisticsServer(ctx, wg)
	}
}

func startRestServer(ctx context.Context, reg *registry.Registry, wg *sync.WaitGroup) {
	ui.Info("Starting REST api server...")

	restServer := api.CreateRestService(reg)
	apiConfig := configuration.CurrentConfig.Api
	restAddress := webserverAddress(apiConfig.Host, apiConfig.Port)

	runWebserver(ctx, wg, "REST api server", restServer, restAddress, func(err error) {
		ui.ErrorAndNotify("REST Error", "Cannot start REST Api endpoint (%s)", err.Error())
	})
}

func startStatisticsServer(ctx context.Context, wg *sync.WaitGroup) {
	ui.Info("Starting statistics server...")

	echoPrometheus := statistics.CreateStatisticsService()
	statisticsConfig := configuration.CurrentConfig.Statistics
	prometheusAddress := webserverAddress(statisticsConfig.Host, statisticsConfig.Port)

	runWebserver(ctx, wg, "Statistics server", echoPrometheus, prometheusAddress, func(err error) {
		ui.ErrorAndNotify("Statistics Error", "Cannot start prometheus metrics endpoint (%s)", err.Error())
	})
}

// runWebserver serves the given echo instance on address in the background until ctx is done,
// then shuts it down gracefully. wg is released once the server has stopped.
func runWebserver(ctx context.Context, wg *sync.WaitGroup, name string, server *echo.Echo, address string, onStartError func(err error)) {
	startConfig := echo.StartConfig{
		Address:    address,
		HideBanner: true,
		HidePort:   true,
		ListenerAddrFunc: func(addr net.Addr) {
			ui.Info("%s listening on %s", name, addr.String())
		},
		BeforeServeFunc: func(s *http.Server) error {
			// echo v5 sets a 30s ReadTimeout by default, echo v4 did not set any timeout
			s.ReadTimeout = 0
			return nil
		},
		GracefulTimeout: webserverShutdownTimeout,
		OnShutdownError: func(err error) {
			ui.Warning("Error stopping %s: %v", name, err)
		},
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		// Start blocks until ctx is done and the graceful shutdown has finished.
		// It returns nil after a regular shutdown and an error only if the server could not be started or failed.
		err := startConfig.Start(ctx, server)
		// a failing Listen is expected if ctx was cancelled before the server could start
		if err != nil && ctx.Err() == nil {
			onStartError(err)
		}
		ui.Debug("%s stopped", name)
	}()
}

func webserverAddress(host string, port int) string {
	return fmt.Sprintf("%s:%d", host, port)
}
