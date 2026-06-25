package internal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/markusressel/fan2go/internal/api"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/statistics"
	"github.com/markusressel/fan2go/internal/ui"
)

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
	if configuration.CurrentConfig.Api.Enabled || configuration.CurrentConfig.Statistics.Enabled {
		ui.Info("Starting Webservers...")
		servers := createWebServer(reg)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ctx.Done()
			ui.Debug("Stopping all webservers...")

			var shutdownWg sync.WaitGroup
			for _, server := range servers {
				shutdownWg.Add(1)
				go func(srv *echo.Echo) {
					defer shutdownWg.Done()
					timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer timeoutCancel()
					if err := srv.Shutdown(timeoutCtx); err != nil {
						ui.Warning("Error stopping webserver: %v", err)
					}
				}(server)
			}
			shutdownWg.Wait()
		}()
	}
}

func createWebServer(reg *registry.Registry) []*echo.Echo {
	result := []*echo.Echo{}
	// Setup Main Server
	if configuration.CurrentConfig.Api.Enabled {
		result = append(result, startRestServer(reg))
	}

	if configuration.CurrentConfig.Statistics.Enabled {
		result = append(result, startStatisticsServer())
	}

	return result
}

func startRestServer(reg *registry.Registry) *echo.Echo {
	ui.Info("Starting REST api server...")

	restServer := api.CreateRestService(reg)

	go func() {
		apiConfig := configuration.CurrentConfig.Api
		restAddress := fmt.Sprintf("%s:%d", apiConfig.Host, apiConfig.Port)

		if err := restServer.Start(restAddress); err != nil && err != http.ErrServerClosed {
			ui.ErrorAndNotify("REST Error", "Cannot start REST Api endpoint (%s)", err.Error())
		}
	}()

	return restServer
}

func startStatisticsServer() *echo.Echo {
	ui.Info("Starting statistics server...")

	echoPrometheus := statistics.CreateStatisticsService()

	go func() {
		prometheusPort := configuration.CurrentConfig.Statistics.Port
		prometheusAddress := fmt.Sprintf(":%d", prometheusPort)

		if err := echoPrometheus.Start(prometheusAddress); err != nil && err != http.ErrServerClosed {
			ui.ErrorAndNotify("Statistics Error", "Cannot start prometheus metrics endpoint (%s)", err.Error())
		}
	}()

	return echoPrometheus
}
