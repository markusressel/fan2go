package internal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"os/user"
	"sync"
	"syscall"
	"time"

	"github.com/markusressel/fan2go/internal/control_loop"

	"github.com/labstack/echo/v4"
	"github.com/markusressel/fan2go/internal/api"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/statistics"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/oklog/run"
)

func RunDaemon() {
	owner, err := getProcessOwner()
	if err != nil {
		ui.Warning("Unable to verify process owner: %v", err)
	} else if owner != "root" {
		ui.Info("fan2go is running as a non-root user '%s'. If you encounter errors, make sure to give this user the required permissions.", owner)
	}

	pers := persistence.NewPersistence(configuration.CurrentConfig.DbPath)

	fanMap, reg, err := InitializeObjects()
	if err != nil {
		ui.Fatal("Error initializing objects: %v", err)
	}

	fanControllers, err := initializeFanControllers(pers, fanMap, reg)
	if err != nil {
		ui.Fatal("Error initializing fan controllers: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fanCtx, cancelFans := context.WithCancel(ctx)
	reloadChan := make(chan struct{}, 1)

	var g run.Group
	{
		if configuration.CurrentConfig.Profiling.Enabled {
			g.Add(func() error {
				return startProfilingWebserver(ctx)
			}, func(err error) {
				if err != nil {
					ui.Warning("Error stopping parca webserver: %v", err)
				} else {
					ui.Debug("Webservers stopped.")
				}
			})
		}
	}

	{
		// === sensor monitoring & fan controllers orchestrator
		g.Add(func() error {
			if len(reg.SnapshotFans()) == 0 {
				ui.FatalWithoutStacktrace("No valid fan configurations, exiting.")
			}

			var fanControllerWg sync.WaitGroup
			startFanControllers(fanCtx, fanControllers, &fanControllerWg)

			defer func() {
				cancelFans()
				fanControllerWg.Wait()
			}()

			for {
				appExiting := false

				func() {
					orchestratorCtx, cancelOrchestrator := context.WithCancel(ctx)
					var orchestratorWg sync.WaitGroup

					defer func() {
						cancelOrchestrator()
						orchestratorWg.Wait()
					}()

					startSensorMonitors(orchestratorCtx, reg, &orchestratorWg)
					startWebservers(orchestratorCtx, reg, &orchestratorWg)

					select {
					case <-ctx.Done():
						appExiting = true
					case <-reloadChan:
						ui.Info("Stopping old sensor monitors and webservers...")
					}
				}()

				if appExiting {
					return nil
				}

				// --- SIGHUP Reload Logic ---
				ui.Info("Reloading configuration...")

				newConfig, err := configuration.LoadConfig()
				if err != nil {
					ui.Error("Configuration parsing failed: %v. Keeping current configuration.", err)
					continue
				}

				err = configuration.Validate(configuration.GetFilePath())
				if err != nil {
					ui.Error("Configuration validation failed: %v. Keeping current configuration.", err)
					continue
				}

				oldConfig := configuration.CurrentConfig
				configuration.CurrentConfig = newConfig

				_, newReg, err := InitializeObjects()
				if err != nil {
					ui.Error("Error re-initializing objects: %v. Rolling back.", err)
					configuration.CurrentConfig = oldConfig
					continue
				}

				ui.Info("Updating fan controllers...")
				for _, ctrl := range fanControllers {
					_ = ctrl
					fanId := ctrl.GetFanId()
					// Find the fan object to get its new curve ID
					var newCurveId string
					for _, fConfig := range configuration.CurrentConfig.Fans {
						if fConfig.ID == fanId {
							newCurveId = fConfig.Curve
							break
						}
					}
					if newCurveId != "" {
						if newCurve, exists := newReg.GetCurve(newCurveId); exists {
							ctrl.UpdateCurve(newCurve)
							ui.Info("Updated curve of fan controller %s to curve %s", fanId, newCurveId)
						} else {
							ui.Warning("New curve %s not found in registry for fan %s", newCurveId, fanId)
						}
					}
				}

				reg = newReg
				ui.Info("Configuration reloaded successfully. Starting new monitors...")
			}
		}, func(err error) {
			cancel()
		})
	}

	{
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

		g.Add(func() error {
			for {
				select {
				case s := <-sig:
					if s == syscall.SIGHUP {
						ui.Info("Received SIGHUP signal, notifying orchestrator...")
						select {
						case reloadChan <- struct{}{}:
						default:
							ui.Warning("Reload already in progress, ignoring SIGHUP.")
						}
					} else {
						ui.Info("Received SIGTERM/SIGINT signal, exiting...")
						return nil
					}
				case <-ctx.Done():
					return nil
				}
			}
		}, func(err error) {
			defer close(sig)
			cancel()
		})
	}

	err = g.Run()

	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ui.Info("Done.")
	os.Exit(0)
}

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
			timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer timeoutCancel()

			for _, server := range servers {
				err := server.Shutdown(timeoutCtx)
				if err != nil {
					ui.Warning("Error stopping webserver: %v", err)
				}
			}
		}()
	}
}

func startSensorMonitors(ctx context.Context, reg *registry.Registry, wg *sync.WaitGroup) {
	// === sensor monitoring
	sensorMapData := reg.SnapshotSensors()
	for _, sensor := range sensorMapData {
		s := sensor
		pollingRate := configuration.CurrentConfig.TempSensorPollingRate
		mon := NewSensorMonitor(s, pollingRate)

		wg.Add(1)
		go func() {
			defer wg.Done()
			err := mon.Run(ctx)
			ui.Info("Sensor Monitor for sensor %s stopped.", s.GetId())
			if err != nil && !errors.Is(err, context.Canceled) {
				ui.Warning("Sensor monitor exited with error: %v", err)
			}
		}()
	}
}

func startFanControllers(ctx context.Context, fanControllers map[fans.Fan]controller.FanController, wg *sync.WaitGroup) {
	// === fan controllers
	for f, c := range fanControllers {
		fan := f
		fanController := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := fanController.Run(ctx)
			ui.Info("Fan controller for fan %s stopped.", fan.GetId())
			if err != nil && !errors.Is(err, context.Canceled) {
				ui.WarningAndNotify(fmt.Sprintf("Fan Controller: %s", fan.GetId()), "Something went wrong: %v", err)
			}
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

func InitializeObjects() (map[configuration.FanConfig]fans.Fan, *registry.Registry, error) {
	controllers := hwmon.GetChips()
	reg := registry.NewRegistry()

	err := initializeSensors(controllers, reg)
	if err != nil {
		return nil, nil, fmt.Errorf("error initializing sensors: %v", err)
	}
	err = initializeCurves(reg)
	if err != nil {
		return nil, nil, fmt.Errorf("error initializing curves: %v", err)
	}

	fanMap, err := initializeFans(controllers, reg)
	if err != nil {
		return nil, nil, fmt.Errorf("error initializing fans: %v", err)
	}

	return fanMap, reg, nil
}

func initializeFanControllers(pers persistence.Persistence, fanMap map[configuration.FanConfig]fans.Fan, reg *registry.Registry) (result map[fans.Fan]controller.FanController, err error) {
	result = map[fans.Fan]controller.FanController{}
	for config, fan := range fanMap {
		updateRate := configuration.CurrentConfig.FanController.AdjustmentTickRate

		var controlLoop control_loop.ControlLoop

		// compatibility fallback
		if config.ControlLoop != nil { //nolint:all
			ui.Warning("Using deprecated control loop configuration for fan %s. Please update your configuration to use the new control algorithm configuration.", config.ID)
			controlLoop = control_loop.NewPidControlLoop(

				config.ControlLoop.P, //nolint:all
				config.ControlLoop.I, //nolint:all
				config.ControlLoop.D, //nolint:all
			)
		} else if config.ControlAlgorithm != nil {
			if config.ControlAlgorithm.Pid != nil {
				controlLoop = control_loop.NewPidControlLoop(
					config.ControlAlgorithm.Pid.P,
					config.ControlAlgorithm.Pid.I,
					config.ControlAlgorithm.Pid.D,
				)
			} else if config.ControlAlgorithm.Direct != nil {
				controlLoop = control_loop.NewDirectControlLoop(
					config.ControlAlgorithm.Direct.MaxPwmChangePerCycle,
				)
			}
		} else {
			controlLoop = control_loop.NewPidControlLoop(
				control_loop.DefaultPidConfig.P,
				control_loop.DefaultPidConfig.I,
				control_loop.DefaultPidConfig.D,
			)
		}

		curve, _ := reg.GetCurve(fan.GetCurveId())
		fanController := controller.NewFanController(pers, fan, curve, controlLoop, updateRate, false)
		result[fan] = fanController
	}

	var fanControllers = []controller.FanController{}
	for _, c := range result {
		fanControllers = append(fanControllers, c)
	}
	controllerCollector := statistics.NewControllerCollector(fanControllers)
	statistics.Register(controllerCollector)

	return result, nil
}

func initializeSensors(controllers []*hwmon.HwMonController, reg *registry.Registry) error {
	var sensorList []sensors.Sensor
	for _, config := range configuration.CurrentConfig.Sensors {
		if config.HwMon != nil {
			err := hwmon.UpdateSensorConfigFromHwMonControllers(controllers, &config)
			if err != nil {
				errMsg := fmt.Sprintf("couldn't find sensor for %s: %v. Skipping.", config.ID, err)
				ui.Warning("%s", errMsg)
				ui.NotifyError("Sensor Skipped", errMsg)
				continue
			}
		}

		sensor, err := sensors.NewSensor(config)
		if err != nil {
			errMsg := fmt.Sprintf("unable to process sensor configuration: %s: %v. Skipping.", config.ID, err)
			ui.Warning("%s", errMsg)
			ui.NotifyError("Sensor Skipped", errMsg)
			continue
		}
		sensorList = append(sensorList, sensor)

		currentValue, err := sensor.GetValue()
		if err != nil {
			ui.Warning("Error reading sensor %s: %v", config.ID, err)
		}
		sensor.SetMovingAvg(currentValue)

		reg.RegisterSensor(sensor)
	}

	sensorCollector := statistics.NewSensorCollector(sensorList)
	statistics.Register(sensorCollector)

	return nil
}

func initializeCurves(reg *registry.Registry) error {
	var curveList []curves.SpeedCurve
	for _, config := range configuration.CurrentConfig.Curves {
		curve, err := curves.NewSpeedCurve(config)
		if err != nil {
			return fmt.Errorf("unable to process curve configuration: %s: %v", config.ID, err)
		}
		curveList = append(curveList, curve)
		reg.RegisterCurve(curve)
	}

	curveCollector := statistics.NewCurveCollector(curveList)
	statistics.Register(curveCollector)

	return nil
}

func initializeFans(controllers []*hwmon.HwMonController, reg *registry.Registry) (map[configuration.FanConfig]fans.Fan, error) {
	var result = map[configuration.FanConfig]fans.Fan{}

	var fanList []fans.Fan

	for _, config := range configuration.CurrentConfig.Fans {
		if config.HwMon != nil {
			err := hwmon.UpdateFanConfigFromHwMonControllers(controllers, &config)
			if err != nil {
				errMsg := fmt.Sprintf("couldn't update fan config from hwmon for %s: %v. Skipping.", config.ID, err)
				ui.Warning("%s", errMsg)
				ui.NotifyError("Fan Skipped", errMsg)
				continue
			}
		}

		fan, err := fans.NewFan(config)
		if err != nil {
			errMsg := fmt.Sprintf("unable to process fan configuration of '%s': %v. Skipping.", config.ID, err)
			ui.Warning("%s", errMsg)
			ui.NotifyError("Fan Skipped", errMsg)
			continue
		}
		reg.RegisterFan(fan)
		result[config] = fan

		fanList = append(fanList, fan)
	}

	fanCollector := statistics.NewFanCollector(fanList)
	statistics.Register(fanCollector)

	return result, nil
}

func getProcessOwner() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	return currentUser.Username, nil
}
