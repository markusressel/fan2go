package internal

import (
	"context"
	"fmt"
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/oklog/run"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	MaxPwmValue       = 255
	MinPwmValue       = 0
	InitialLastSetPwm = -10
)

var (
	SensorMap = map[string]Sensor{}
	FanMap    = map[string]Fan{}
)

type HwMonController struct {
	Name     string   `json:"name"`
	DType    string   `json:"dtype"`
	Modalias string   `json:"modalias"`
	Platform string   `json:"platform"`
	Path     string   `json:"path"`
	Fans     []Fan    `json:"fans"`
	Sensors  []Sensor `json:"sensors"`
}

func Run() {
	if getProcessOwner() != "root" {
		ui.Fatal("Fan control requires root permissions to be able to modify fan speeds, please run fan2go as root")
	}

	persistence := NewPersistence(configuration.CurrentConfig.DbPath)

	controllers, err := FindControllers()
	if err != nil {
		ui.Fatal("Error detecting devices: %s", err.Error())
	}
	MapConfigToControllers(controllers)
	for _, curveConfig := range configuration.CurrentConfig.Curves {
		NewSpeedCurve(curveConfig)
	}

	ctx, cancel := context.WithCancel(context.Background())

	var g run.Group
	{
		// === sensor monitoring
		for _, controller := range controllers {
			for _, s := range controller.Sensors {
				if s.GetConfig() == nil {
					ui.Info("Ignoring unconfigured sensor %s/%s", controller.Name, s.GetLabel())
					continue
				}

				pollingRate := configuration.CurrentConfig.TempSensorPollingRate
				mon := NewSensorMonitor(s, pollingRate)

				g.Add(func() error {
					return mon.Run(ctx)
				}, func(err error) {
					ui.Fatal("Error monitoring sensor: %v", err)
				})
			}
		}
	}
	{
		// === fan controllers
		count := 0
		for _, controller := range controllers {
			for _, f := range controller.Fans {
				fan := f
				if fan.GetConfig() == nil {
					// this fan is not configured, ignore it
					ui.Info("Ignoring unconfigured fan %s/%s", controller.Name, fan.GetName())
					continue
				}

				fanId := fan.GetConfig().ID

				updateRate := configuration.CurrentConfig.ControllerAdjustmentTickRate
				fanController := NewFanController(persistence, fan, updateRate)

				g.Add(func() error {
					rpmTick := time.Tick(configuration.CurrentConfig.RpmPollingRate)
					return rpmMonitor(ctx, fanId, rpmTick)
				}, func(err error) {
					// nothing to do here
				})

				g.Add(func() error {
					return fanController.Run(ctx)
				}, func(err error) {
					if err != nil {
						ui.Error("Something went wrong: %v", err)
					}

					ui.Info("Trying to restore fan settings for %s...", fanId)

					// TODO: move this error handling to the FanController implementation

					// try to reset the pwm_enable value
					if fan.GetOriginalPwmEnabled() != 1 {
						err := fan.SetPwmEnabled(fan.GetOriginalPwmEnabled())
						if err == nil {
							return
						}
					}
					err = setPwm(fan, MaxPwmValue)
					if err != nil {
						ui.Warning("Unable to restore fan %s, make sure it is running!", fan.GetConfig().ID)
					}
				})
				count++
			}
		}

		if count == 0 {
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

func rpmMonitor(ctx context.Context, fanId string, tick <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			measureRpm(fanId)
		}
	}
}

func getProcessOwner() string {
	stdout, err := exec.Command("ps", "-o", "user=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		ui.Error("%v", err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(stdout))
}

// MapConfigToControllers maps detected devices to configuration values
func MapConfigToControllers(controllers []*HwMonController) {
	for _, controller := range controllers {
		// match fan and fan config entries
		for _, fan := range controller.Fans {
			fanConfig := findFanConfig(controller, fan)
			if fanConfig != nil {
				ui.Debug("Mapping fan config %s to %s", fanConfig.ID, fan.(*fans.HwMonFan).PwmOutput)
				fan.SetConfig(fanConfig)
				FanMap[fanConfig.ID] = fan
			}
		}
		// match sensor and sensor config entries
		for _, sensor := range controller.Sensors {
			sensorConfig := findSensorConfig(controller, sensor)
			if sensorConfig == nil {
				continue
			}

			ui.Debug("Mapping sensor config %s to %s", sensorConfig.ID, sensor.(*sensors.HwmonSensor).Input)

			sensor.SetConfig(sensorConfig)
			// remember ID -> Sensor association for later
			SensorMap[sensorConfig.ID] = sensor

			// initialize arrays for storing temps
			currentValue, err := sensor.GetValue()
			if err != nil {
				ui.Fatal("Error reading sensor %s: %v", sensorConfig.ID, err)
			}
			sensor.SetMovingAvg(currentValue)
		}
	}
}

// read the current value of a fan RPM sensor and append it to the moving window
func measureRpm(fanId string) {
	fan := FanMap[fanId]

	pwm := fan.GetPwm()
	rpm := fan.GetRpm()

	ui.Debug("Measured RPM of %d at PWM %d for fan %s", rpm, pwm, fan.GetConfig().ID)

	updatedRpmAvg := updateSimpleMovingAvg(fan.GetRpmAvg(), configuration.CurrentConfig.RpmRollingWindowSize, float64(rpm))
	fan.SetRpmAvg(updatedRpmAvg)

	pwmRpmMap := fan.GetFanCurveData()
	pointWindow, exists := (*pwmRpmMap)[pwm]
	if !exists {
		// create rolling window for current pwm value
		pointWindow = createRollingWindow(configuration.CurrentConfig.RpmRollingWindowSize)
		(*pwmRpmMap)[pwm] = pointWindow
	}
	pointWindow.Append(float64(rpm))
}

func findFanConfig(controller *HwMonController, fan Fan) (fanConfig *configuration.FanConfig) {
	for _, fanConfig := range configuration.CurrentConfig.Fans {

		if fanConfig.HwMon != nil {
			c := fanConfig.HwMon
			hwmonFan := fan.(*fans.HwMonFan)

			if controller.Platform == c.Platform &&
				hwmonFan.Index == c.Index {
				return &fanConfig
			}
		} else if fanConfig.File != nil {
			// TODO
		}
	}
	return nil
}

func findSensorConfig(controller *HwMonController, sensor Sensor) (sensorConfig *configuration.SensorConfig) {
	for _, sensorConfig := range configuration.CurrentConfig.Sensors {

		if sensorConfig.HwMon != nil {
			c := sensorConfig.HwMon
			hwmonFan := sensor.(*sensors.HwmonSensor)

			if controller.Platform == c.Platform &&
				hwmonFan.Index == c.Index {
				return &sensorConfig
			}
		} else if sensorConfig.File != nil {
			// TODO
		}

	}
	return nil
}

// FindControllers Finds controllers and fans
func FindControllers() (controllers []*HwMonController, err error) {
	hwmonDevices := util.FindHwmonDevicePaths()
	i2cDevices := util.FindI2cDevicePaths()
	allDevices := append(hwmonDevices, i2cDevices...)

	for _, devicePath := range allDevices {

		var deviceName = util.GetDeviceName(devicePath)
		var identifier = computeIdentifier(devicePath, deviceName)

		dType := util.GetDeviceType(devicePath)
		modalias := util.GetDeviceModalias(devicePath)
		platform := findPlatform(devicePath)
		if len(platform) <= 0 {
			platform = identifier
		}

		fanList := createFans(devicePath)
		sensorList := createSensors(devicePath)

		if len(fanList) <= 0 && len(sensorList) <= 0 {
			continue
		}

		controller := &HwMonController{
			Name:     identifier,
			DType:    dType,
			Modalias: modalias,
			Platform: platform,
			Path:     devicePath,
			Fans:     fanList,
			Sensors:  sensorList,
		}
		controllers = append(controllers, controller)
	}

	return controllers, err
}

func findPlatform(devicePath string) string {
	platformRegex := regexp.MustCompile(".*/platform/{}/.*")
	return platformRegex.FindString(devicePath)
}

func computeIdentifier(devicePath string, deviceName string) (name string) {
	pciDeviceRegex := regexp.MustCompile("\\w+:\\w{2}:\\w{2}\\.\\d")

	if len(name) <= 0 {
		name = deviceName
	}

	if len(name) <= 0 {
		_, name = filepath.Split(devicePath)
	}

	if strings.Contains(devicePath, "/pci") {
		// add pci suffix to name
		matches := pciDeviceRegex.FindAllString(devicePath, -1)
		if len(matches) > 0 {
			lastMatch := matches[len(matches)-1]
			pciIdentifier := util.CreateShortPciIdentifier(lastMatch)
			name = fmt.Sprintf("%s-%s", name, pciIdentifier)
		}
	}

	return name
}

// creates fan objects for the given device path
func createFans(devicePath string) (fanList []Fan) {
	inputs := util.FindFilesMatching(devicePath, "^fan[1-9]_input$")
	outputs := util.FindFilesMatching(devicePath, "^pwm[1-9]$")

	for idx, output := range outputs {
		_, file := filepath.Split(output)

		label := util.GetLabel(devicePath, output)

		index, err := strconv.Atoi(file[len(file)-1:])
		if err != nil {
			ui.Fatal("%v", err)
		}

		fan := &fans.HwMonFan{
			Name:         file,
			Label:        label,
			Index:        index,
			PwmOutput:    output,
			RpmInput:     inputs[idx],
			RpmMovingAvg: 0,
			MinPwm:       MinPwmValue,
			MaxPwm:       MaxPwmValue,
			FanCurveData: &map[int]*rolling.PointPolicy{},
			LastSetPwm:   InitialLastSetPwm,
		}

		// store original pwm_enable value
		pwmEnabled, err := fan.GetPwmEnabled()
		if err != nil {
			ui.Fatal("Cannot read pwm_enable value of %s", fan.GetConfig().ID)
		}
		fan.OriginalPwmEnabled = pwmEnabled

		fanList = append(fanList, fan)
	}

	return fanList
}

// creates sensor objects for the given device path
func createSensors(devicePath string) (result []Sensor) {
	inputs := util.FindFilesMatching(devicePath, "^temp[1-9]_input$")

	for _, input := range inputs {
		_, file := filepath.Split(input)
		label := util.GetLabel(devicePath, file)

		index, err := strconv.Atoi(string(file[4]))
		if err != nil {
			ui.Fatal("%v", err)
		}

		sensor := &sensors.HwmonSensor{
			Name:  file,
			Label: label,
			Index: index,
			Input: input,
		}
		result = append(result, sensor)
	}

	return result
}

func createRollingWindow(size int) *rolling.PointPolicy {
	return rolling.NewPointPolicy(rolling.NewWindow(size))
}

// returns the average of all values in the window
func getWindowAvg(window *rolling.PointPolicy) float64 {
	return window.Reduce(rolling.Avg)
}
