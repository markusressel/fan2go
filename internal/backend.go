package internal

import (
	"context"
	"fmt"
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
	"github.com/markusressel/fan2go/internal/util"
	"github.com/oklog/run"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func RunDaemon() {
	if getProcessOwner() != "root" {
		ui.Fatal("Fan control requires root permissions to be able to modify fan speeds, please run fan2go as root")
	}

	pers := persistence.NewPersistence(configuration.CurrentConfig.DbPath)

	fanControllers := initializeObjects(pers)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var g run.Group
	{
		enabled := configuration.CurrentConfig.Api.Enabled
		if enabled {
			g.Add(func() error {
				ui.Info("Starting REST server...")

				apiConfig := configuration.CurrentConfig.Api
				server := api.CreateRestService()
				address := fmt.Sprintf("%s:%d", apiConfig.Host, apiConfig.Port)

				go func() {
					if err := server.Start(address); err != nil && err != http.ErrServerClosed {
						ui.Fatal("Error starting REST server: %v", err)
					}
				}()

				select {
				case <-ctx.Done():
					ui.Info("Stopping REST server...")
					timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 5*time.Second)
					defer timeoutCancel()
					return server.Shutdown(timeoutCtx)
				}
			}, func(err error) {
				if err != nil {
					ui.Warning("Error stopping REST server: " + err.Error())
				} else {
					ui.Info("REST server stopped.")
				}
			})
		}
	}
	{
		enabled := configuration.CurrentConfig.Statistics.Enabled
		if enabled {
			// === Prometheus Exporter
			g.Add(func() error {
				port := configuration.CurrentConfig.Statistics.Port

				endpoint := "/metrics"
				addr := fmt.Sprintf(":%d", port)

				handler := promhttp.Handler()
				http.Handle(endpoint, handler)

				server := &http.Server{
					Addr:         addr,
					Handler:      handler,
					ReadTimeout:  1 * time.Second,
					WriteTimeout: 1 * time.Second,
				}

				go func() {
					ui.Info("Starting statistics server...")
					if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
						ui.ErrorAndNotify("Statistics Error", "Cannot start prometheus metrics endpoint (%s)", err.Error())
					}
				}()

				select {
				case <-ctx.Done():
					ui.Info("Stopping statistics server...")
					timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 5*time.Second)
					defer timeoutCancel()
					return server.Shutdown(timeoutCtx)
				}
			}, func(err error) {
				if err != nil {
					ui.Warning("Error stopping statistics server: " + err.Error())
				} else {
					ui.Info("Statistics server stopped.")
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
			ui.Fatal("No valid fan configurations, exiting.")
		}
	}
	{
		sig := make(chan os.Signal)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM, os.Kill)

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

func initializeObjects(pers persistence.Persistence) map[fans.Fan]controller.FanController {
	controllers := hwmon.GetChips()

	initializeSensors(controllers)
	initializeCurves()

	var result = map[fans.Fan]controller.FanController{}

	for config, fan := range initializeFans(controllers) {
		updateRate := configuration.CurrentConfig.ControllerAdjustmentTickRate

		var pidLoop util.PidLoop
		if config.ControlLoop != nil {
			pidLoop = *util.NewPidLoop(
				config.ControlLoop.P,
				config.ControlLoop.I,
				config.ControlLoop.D,
			)
		} else {
			pidLoop = *util.NewPidLoop(
				0.03,
				0.002,
				0.0005,
			)
		}
		fanController := controller.NewFanController(pers, fan, pidLoop, updateRate)
		result[fan] = fanController
	}

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
			found := false
			for _, c := range controllers {
				matched, err := regexp.MatchString("(?i)"+config.HwMon.Platform, c.Platform)
				if err != nil {
					ui.Fatal("Failed to match platform regex of %s (%s) against controller platform %s", config.ID, config.HwMon.Platform, c.Platform)
				}
				if matched {
					found = true
					fan := c.Fans[config.HwMon.Index].Config.HwMon
					config.HwMon.PwmOutput = fan.PwmOutput
					config.HwMon.RpmInput = fan.RpmInput
					break
				}
			}
			if !found {
				ui.Fatal("Couldn't find hwmon device with platform '%s' for fan: %s", config.HwMon.Platform, config.ID)
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

func getProcessOwner() string {
	stdout, err := exec.Command("ps", "-o", "user=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		ui.Fatal("Error checking process owner: %v", err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(stdout))
}
