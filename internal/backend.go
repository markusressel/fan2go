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
	"strconv"
	"strings"
	"syscall"
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
				http.Handle(endpoint, promhttp.Handler())
				if err := http.ListenAndServe(addr, nil); err != nil {
					ui.Error("Cannot start prometheus metrics endpoint (%s)", err.Error())
				}
				select {}
			}, func(err error) {
				if err != nil {
					ui.Warning("Error ")
				}
			})
		}
	}
	{
		// === sensor monitoring
		for _, sensor := range sensors.SensorMap {
			pollingRate := configuration.CurrentConfig.TempSensorPollingRate
			mon := NewSensorMonitor(sensor, pollingRate)

			g.Add(func() error {
				err := mon.Run(ctx)
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
			updateRate := configuration.CurrentConfig.ControllerAdjustmentTickRate
			fanController := controller.NewFanController(pers, fan, updateRate)

			g.Add(func() error {
				err := fanController.Run(ctx)
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
			ui.Info("Exiting...")
			return nil
		}, func(err error) {
			cancel()
			close(sig)
		})
	}

	if err := g.Run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func InitializeObjects() {
	controllers := hwmon.GetChips()

	var sensorList []sensors.Sensor
	for _, config := range configuration.CurrentConfig.Sensors {
		if config.HwMon != nil {
			found := false
			for _, c := range controllers {
				if c.Platform == config.HwMon.Platform {
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

	for _, config := range configuration.CurrentConfig.Curves {
		curve, err := curves.NewSpeedCurve(config)
		if err != nil {
			ui.Fatal("Unable to process curve configuration: %s", config.ID)
		}
		curves.SpeedCurveMap[config.ID] = curve
	}

	var fanList []fans.Fan
	for _, config := range configuration.CurrentConfig.Fans {
		if config.HwMon != nil {
			found := false
			for _, c := range controllers {
				if c.Platform == config.HwMon.Platform {
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
