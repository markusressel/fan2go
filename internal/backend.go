package internal

import (
	"context"
	"fmt"
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

	InitializeObjects()

	ctx, cancel := context.WithCancel(context.Background())

	var g run.Group
	{
		enabled := configuration.CurrentConfig.Statistics.Enabled
		if enabled {
			// === Prometheus Exporter
			g.Add(func() error {
				port := configuration.CurrentConfig.Statistics.Port
				if port <= 0 || port >= 65535 {
					port = 9000
				}
				endpoint := "/metrics"
				addr := fmt.Sprintf(":%d", port)
				handler := promhttp.Handler()
				http.Handle(endpoint, handler)
				server := &http.Server{Addr: addr, Handler: handler}
				if err := server.ListenAndServe(); err != nil {
					ui.Error("Cannot start prometheus metrics endpoint (%s)", err.Error())
				}

				select {
				case <-ctx.Done():
					ui.Info("Stopping statistics server...")
					timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		for _, fan := range fans.FanMap {
			f := fan
			updateRate := configuration.CurrentConfig.ControllerAdjustmentTickRate
			fanController := controller.NewFanController(pers, f, updateRate)

			g.Add(func() error {
				err := fanController.Run(ctx)
				ui.Info("Fan controller for fan %s stopped.", f.GetId())
				if err != nil {
					panic(err)
				}
				return err
			}, func(err error) {
				if err != nil {
					ui.Warning("Something went wrong: %v", err)
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

func InitializeObjects() {
	controllers := hwmon.GetChips()

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
					config.HwMon.TempInput = c.Sensors[config.HwMon.Index-1].Input
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
					index := config.HwMon.Index - 1
					if len(c.Fans) > index {
						fan := c.Fans[index]
						config.HwMon.PwmOutput = fan.PwmOutput
						config.HwMon.RpmInput = fan.RpmInput
					}

					break
				}
			}
			if !found {
				ui.Fatal("Couldn't find hwmon device with platform '%s' for fan: %s", config.HwMon.Platform, config.ID)
			}
		}

		fan, err := fans.NewFan(config)
		if err != nil {
			ui.Fatal("Unable to process fan configuration: %s", config.ID)
		}
		fans.FanMap[config.ID] = fan

		fanList = append(fanList, fan)
	}

	fanCollector := statistics.NewFanCollector(fanList)
	statistics.Register(fanCollector)
}

func getProcessOwner() string {
	stdout, err := exec.Command("ps", "-o", "user=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		ui.Fatal("Error checking process owner: %v", err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(stdout))
}
