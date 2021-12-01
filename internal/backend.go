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
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/oklog/run"
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
		// === sensor monitoring
		for _, sensor := range sensors.SensorMap {
			pollingRate := configuration.CurrentConfig.TempSensorPollingRate
			mon := NewSensorMonitor(sensor, pollingRate)

			g.Add(func() error {
				return mon.Run(ctx)
			}, func(err error) {
				if err != nil {
					ui.Error("Error monitoring sensor: %v", err)
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
				return fanController.Run(ctx)
			}, func(err error) {
				if err != nil {
					ui.Error("Something went wrong: %v", err)
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

		currentValue, err := sensor.GetValue()
		if err != nil {
			ui.Warning("Error reading sensor %s: %v", config.ID, err)
		}
		sensor.SetMovingAvg(currentValue)

		sensors.SensorMap[config.ID] = sensor
	}

	for _, config := range configuration.CurrentConfig.Curves {
		curve, err := curves.NewSpeedCurve(config)
		if err != nil {
			ui.Fatal("Unable to process curve configuration: %s", config.ID)
		}
		curves.SpeedCurveMap[config.ID] = curve
	}

	for _, config := range configuration.CurrentConfig.Fans {
		if config.HwMon != nil {
			found := false
			for _, c := range controllers {
				if c.Platform == config.HwMon.Platform {
					found = true
					config.HwMon.PwmOutput = c.Fans[config.HwMon.Index-1].PwmOutput
					config.HwMon.RpmInput = c.Fans[config.HwMon.Index-1].RpmInput
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
	}

}

func getProcessOwner() string {
	stdout, err := exec.Command("ps", "-o", "user=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		ui.Fatal("%v", err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(stdout))
}
