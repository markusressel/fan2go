package internal

import (
	"context"
	"fmt"
	"github.com/markusressel/fan2go/internal/control_loop"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"os/user"
	"regexp"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/markusressel/fan2go/internal/api"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/persistence"
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

	fanControllers := initializeObjects(pers)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var g run.Group
	{
		if configuration.CurrentConfig.Profiling.Enabled {
			g.Add(func() error {
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
			}, func(err error) {
				if err != nil {
					ui.Warning("Error stopping parca webserver: " + err.Error())
				} else {
					ui.Debug("Webservers stopped.")
				}
			})
		}
	}
	{
		// === Global Webserver
		if configuration.CurrentConfig.Api.Enabled || configuration.CurrentConfig.Statistics.Enabled {
			g.Add(func() error {
				ui.Info("Starting Webserver...")

				servers := createWebServer()

				<-ctx.Done()
				ui.Debug("Stopping all webservers...")
				timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 5*time.Second)
				defer timeoutCancel()

				for _, server := range servers {
					err := server.Shutdown(timeoutCtx)
					if err != nil {
						return err
					}
				}
				return nil
			}, func(err error) {
				if err != nil {
					ui.Warning("Error stopping webservers: " + err.Error())
				} else {
					ui.Debug("Webservers stopped.")
				}
			})
		}
	}
	{
		// === sensor monitoring
		for _, sensor := range sensors.SensorMap {
			s := sensor
			pollingRate := configuration.CurrentConfig.TempSensorPollingRate
			mon := NewSensorMonitor(s, pollingRate)

			g.Add(func() error {
				err := mon.Run(ctx)
				ui.Info("Sensor Monitor for sensor %s stopped.", s.GetId())
				if err != nil {
					panic(err)
				}
				return err
			}, func(err error) {
				if err != nil {
					ui.Warning("Error monitoring sensor: %v", err)
				}
			})
		}
	}
	{
		// === fan controllers
		for f, c := range fanControllers {
			fan := f
			fanController := c
			g.Add(func() error {
				err := fanController.Run(ctx)
				ui.Info("Fan controller for fan %s stopped.", fan.GetId())
				if err != nil {
					ui.NotifyError(fmt.Sprintf("Fan Controller: %s", fan.GetId()), err.Error())
					panic(err)
				}
				return err
			}, func(err error) {
				if err != nil {
					ui.WarningAndNotify(fmt.Sprintf("Fan Controller: %s", fan.GetId()), "Something went wrong: %v", err)
				}
			})
		}

		if len(fans.FanMap) == 0 {
			ui.FatalWithoutStacktrace("No valid fan configurations, exiting.")
		}
	}
	{
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

		g.Add(func() error {
			<-sig
			ui.Info("Received SIGTERM signal, exiting...")
			return nil
		}, func(err error) {
			defer close(sig)
			cancel()
		})
	}

	if err := g.Run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	} else {
		ui.Info("Done.")
		os.Exit(0)
	}
}

func createWebServer() []*echo.Echo {
	result := []*echo.Echo{}
	// Setup Main Server
	if configuration.CurrentConfig.Api.Enabled {
		result = append(result, startRestServer())
	}

	if configuration.CurrentConfig.Statistics.Enabled {
		result = append(result, startStatisticsServer())
	}

	return result
}

func startRestServer() *echo.Echo {
	ui.Info("Starting REST api server...")

	restServer := api.CreateRestService()

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

func initializeObjects(pers persistence.Persistence) map[fans.Fan]controller.FanController {
	controllers := hwmon.GetChips()

	initializeSensors(controllers)
	initializeCurves()

	var result = map[fans.Fan]controller.FanController{}

	for config, fan := range initializeFans(controllers) {
		updateRate := configuration.CurrentConfig.ControllerAdjustmentTickRate

		var controlLoop control_loop.ControlLoop

		// compatibility fallback
		if config.ControlLoop != nil {
			ui.Warning("Using deprecated control loop configuration for fan %s. Please update your configuration to use the new control algorithm configuration.", config.ID)
			controlLoop = control_loop.NewPidControlLoop(
				config.ControlLoop.P,
				config.ControlLoop.I,
				config.ControlLoop.D,
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
			controlLoop = control_loop.NewDirectControlLoop(nil)
		}

		fanController := controller.NewFanController(pers, fan, controlLoop, updateRate)
		result[fan] = fanController
	}

	var fanControllers = []controller.FanController{}
	for _, c := range result {
		fanControllers = append(fanControllers, c)
	}
	controllerCollector := statistics.NewControllerCollector(fanControllers)
	statistics.Register(controllerCollector)

	return result
}

func initializeSensors(controllers []*hwmon.HwMonController) {
	var sensorList []sensors.Sensor
	for _, config := range configuration.CurrentConfig.Sensors {
		if config.HwMon != nil {
			found := false
			for _, c := range controllers {
				matched, err := regexp.MatchString("(?i)"+config.HwMon.Platform, c.Platform)
				if err != nil {
					ui.Fatal("Failed to match platform regex of %s (%s) against controller platform %s", config.ID, config.HwMon.Platform, c.Platform)
				}
				if matched {
					found = true
					config.HwMon.TempInput = c.Sensors[config.HwMon.Index].Input
				}
			}
			if !found {
				ui.Fatal("Couldn't find hwmon device with platform '%s' for sensor: %s. Run 'fan2go detect' again and correct any mistake.", config.HwMon.Platform, config.ID)
			}
		}

		sensor, err := sensors.NewSensor(config)
		if err != nil {
			ui.Fatal("Unable to process sensor configuration: %s", config.ID)
		}
		sensorList = append(sensorList, sensor)

		currentValue, err := sensor.GetValue()
		if err != nil {
			ui.Warning("Error reading sensor %s: %v", config.ID, err)
		}
		sensor.SetMovingAvg(currentValue)

		sensors.SensorMap[config.ID] = sensor
	}

	sensorCollector := statistics.NewSensorCollector(sensorList)
	statistics.Register(sensorCollector)
}

func initializeCurves() {
	var curveList []curves.SpeedCurve
	for _, config := range configuration.CurrentConfig.Curves {
		curve, err := curves.NewSpeedCurve(config)
		if err != nil {
			ui.Fatal("Unable to process curve configuration: %s", config.ID)
		}
		curveList = append(curveList, curve)
		curves.SpeedCurveMap[config.ID] = curve
	}

	curveCollector := statistics.NewCurveCollector(curveList)
	statistics.Register(curveCollector)
}

func initializeFans(controllers []*hwmon.HwMonController) map[configuration.FanConfig]fans.Fan {
	var result = map[configuration.FanConfig]fans.Fan{}

	var fanList []fans.Fan

	for _, config := range configuration.CurrentConfig.Fans {
		if config.HwMon != nil {
			err := hwmon.UpdateFanConfigFromHwMonControllers(controllers, &config)
			if err != nil {
				ui.Fatal("Couldn't update fan config from hwmon: %s", err)
			}
		}

		fan, err := fans.NewFan(config)
		if err != nil {
			ui.Fatal("Unable to process fan configuration of '%s': %v", config.ID, err)
		}
		fans.FanMap[config.ID] = fan
		result[config] = fan

		fanList = append(fanList, fan)
	}

	fanCollector := statistics.NewFanCollector(fanList)
	statistics.Register(fanCollector)

	return result
}

func getProcessOwner() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	return currentUser.Username, nil
}
