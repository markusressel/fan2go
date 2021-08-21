package internal

import (
	"context"
	"errors"
	"fmt"
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/oklog/run"
	bolt "go.etcd.io/bbolt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
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
	SensorMap = map[string]*Sensor{}
	Verbose   bool
)

func Run(verbose bool) {
	Verbose = verbose
	// TODO: maybe it is possible without root by providing permissions?
	if getProcessOwner() != "root" {
		log.Fatalf("Fan control requires root access, please run fan2go as root")
	}

	db := OpenPersistence(CurrentConfig.DbPath)
	defer db.Close()

	controllers, err := FindControllers()
	if err != nil {
		log.Fatalf("Error detecting devices: %s", err.Error())
	}
	mapConfigToControllers(controllers)

	ctx, cancel := context.WithCancel(context.Background())

	var g run.Group
	{
		// === sensor monitoring
		tempTick := time.Tick(CurrentConfig.TempSensorPollingRate)

		for _, controller := range controllers {
			for _, s := range controller.Sensors {
				sensor := s
				if sensor.Config == nil {
					log.Printf("Ignoring unconfigured sensor %s/%s", controller.Name, sensor.Name)
					continue
				}

				g.Add(func() error {
					return sensorMonitor(ctx, sensor, tempTick)
				}, func(err error) {
					// nothing to do here
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
				if fan.Config == nil {
					// this fan is not configured, ignore it
					log.Printf("Ignoring unconfigured fan %s/%s", controller.Name, fan.Name)
					continue
				}

				g.Add(func() error {
					rpmTick := time.Tick(CurrentConfig.RpmPollingRate)
					return rpmMonitor(ctx, fan, rpmTick)
				}, func(err error) {
					// nothing to do here
				})

				g.Add(func() error {
					log.Printf("Gathering data...")
					// wait a bit to gather monitoring data
					time.Sleep(2*time.Second + CurrentConfig.TempSensorPollingRate*2)

					tick := time.Tick(CurrentConfig.ControllerAdjustmentTickRate)
					return fanController(ctx, db, fan, tick)
				}, func(err error) {
					log.Printf("Trying to restore fan settings for %s...", fan.Config.Id)

					// try to reset the pwm_enabled value
					if fan.OriginalPwmEnabled != 1 {
						err := setPwmEnabled(fan, fan.OriginalPwmEnabled)
						if err == nil {
							return
						}
					}
					err = setPwm(fan, MaxPwmValue)
					if err != nil {
						log.Printf("WARNING: Unable to revert fan %s, make sure it is running!", fan.Config.Id)
					}
				})
				count++
			}
		}

		if count == 0 {
			log.Fatal("No valid fan configurations, exiting.")
		}
	}
	{
		sig := make(chan os.Signal)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM, os.Kill)

		g.Add(func() error {
			<-sig
			log.Println("Exiting...")
			return nil
		}, func(err error) {
			cancel()
			close(sig)
		})
	}

	if err := g.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func rpmMonitor(ctx context.Context, fan *Fan, tick <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			measureRpm(fan)
		}
	}
}

func sensorMonitor(ctx context.Context, sensor *Sensor, tick <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			err := updateSensor(sensor)
			if err != nil {
				log.Fatal(err)
			}
		}
	}
}

func getProcessOwner() string {
	stdout, err := exec.Command("ps", "-o", "user=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(stdout))
}

// Map detect devices to configuration values
func mapConfigToControllers(controllers []*Controller) {
	for _, controller := range controllers {
		// match fan and fan config entries
		for _, fan := range controller.Fans {
			fanConfig := findFanConfig(controller, fan)
			if fanConfig != nil {
				if Verbose {
					log.Printf("Mapping fan config %s to %s", fanConfig.Id, fan.PwmOutput)
				}
				fan.Config = fanConfig
			}
		}
		// match sensor and sensor config entries
		for _, sensor := range controller.Sensors {
			sensorConfig := findSensorConfig(controller, sensor)
			if sensorConfig != nil {
				if Verbose {
					log.Printf("Mapping sensor config %s to %s", sensorConfig.Id, sensor.Input)
				}

				sensor.Config = sensorConfig

				// remember ID -> Sensor association for later
				SensorMap[sensorConfig.Id] = sensor

				// initialize arrays for storing temps
				currentValue, err := util.ReadIntFromFile(sensor.Input)
				if err != nil {
					log.Fatalf("Error reading sensor %s: %s", sensorConfig.Id, err.Error())
				}
				sensor.MovingAvg = float64(currentValue)
			}
		}
	}
}

// read the current value of a fan RPM sensor and append it to the moving window
func measureRpm(fan *Fan) {
	pwm := GetPwm(fan)
	rpm := GetRpm(fan)

	if Verbose {
		log.Printf("Measured RPM of %d at PWM %d for fan %s", rpm, pwm, fan.Config.Id)
	}

	fan.RpmMovingAvg = updateSimpleMovingAvg(fan.RpmMovingAvg, CurrentConfig.RpmRollingWindowSize, float64(rpm))

	pwmRpmMap := fan.FanCurveData
	pointWindow, ok := (*pwmRpmMap)[pwm]
	if !ok {
		// create rolling window for current pwm value
		pointWindow = rolling.NewPointPolicy(rolling.NewWindow(CurrentConfig.RpmRollingWindowSize))
		(*pwmRpmMap)[pwm] = pointWindow
	}
	pointWindow.Append(float64(rpm))
}

func updatePwmBoundaries(fan *Fan) {
	startPwm := 255
	maxPwm := 255
	pwmRpmMap := fan.FanCurveData
	if pwmRpmMap == nil {
		// we have no data yet
		startPwm = 0
	} else {
		// get pwm keys that we have data for
		keys := make([]int, len(*pwmRpmMap))
		i := 0
		for k := range *pwmRpmMap {
			keys[i] = k
			i++
		}
		// sort them increasing
		sort.Ints(keys)

		maxRpm := 0
		for _, pwm := range keys {
			window := (*pwmRpmMap)[pwm]
			avgRpm := int(window.Reduce(rolling.Avg))

			if avgRpm > maxRpm {
				maxRpm = avgRpm
				maxPwm = pwm
			}

			if avgRpm > 0 && pwm < startPwm {
				startPwm = pwm
			}
		}
	}

	log.Printf("Start PWM of %s (%s): %d", fan.Config.Id, fan.Name, startPwm)
	fan.StartPwm = startPwm
	log.Printf("Max PWM of %s (%s): %d", fan.Config.Id, fan.Name, maxPwm)
	fan.MaxPwm = maxPwm
}

// read the current value of a sensor and append it to the moving window
func updateSensor(sensor *Sensor) (err error) {
	value, err := util.ReadIntFromFile(sensor.Input)
	if err != nil {
		return err
	}

	var n = CurrentConfig.TempRollingWindowSize
	sensor.MovingAvg = updateSimpleMovingAvg(sensor.MovingAvg, n, float64(value))
	if value > int(sensor.Config.Max) {
		// if the value is higher than the specified max temperature,
		// insert the value twice into the moving window,
		// to give it a bigger impact
		sensor.MovingAvg = updateSimpleMovingAvg(sensor.MovingAvg, n, float64(value))
	}

	return nil
}

// goroutine to continuously adjust the speed of a fan
func fanController(ctx context.Context, db *bolt.DB, fan *Fan, tick <-chan time.Time) error {
	err := trySetManualPwm(fan)
	if err != nil {
		log.Printf("Could not enable fan control on %s (%s)", fan.Config.Id, fan.Name)
		return err
	}

	// check if we have data for this fan in persistence,
	// if not we need to run the initialization sequence
	log.Printf("Loading fan curve data for fan '%s'...", fan.Config.Id)
	err = LoadFanPwmData(db, fan)
	if err != nil {
		log.Printf("No fan curve data found for fan '%s', starting initialization sequence...", fan.Config.Id)
		runInitializationSequence(db, fan)
	}
	updatePwmBoundaries(fan)

	log.Printf("Starting controller loop for fan '%s'", fan.Config.Id)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			err = setOptimalFanSpeed(fan)
			if err != nil {
				log.Printf("Error setting %s (%s): %s", fan.Config.Id, fan.Name, err.Error())
				err = trySetManualPwm(fan)
				if err != nil {
					log.Printf("Could not enable fan control on %s (%s)", fan.Config.Id, fan.Name)
					return err
				}
			}
		}
	}
}

func trySetManualPwm(fan *Fan) (err error) {
	err = setPwmEnabled(fan, 1)
	if err != nil {
		err = setPwmEnabled(fan, 0)
		if err != nil {
			return err
		}
	}
	return nil
}

// runs an initialization sequence for the given fan
// to determine an estimation of its fan curve
func runInitializationSequence(db *bolt.DB, fan *Fan) {
	for pwm := 0; pwm < MaxPwmValue; pwm++ {
		// set a pwm
		err := util.WriteIntToFile(pwm, fan.PwmOutput)
		if err != nil {
			log.Fatalf("Unable to run initialization sequence on %s (%s): %s", fan.Config.Id, fan.Name, err.Error())
		}

		if pwm == 0 {
			// wait an additional 2 seconds, to make sure the fans
			// have time to spin down even from max speed to 0
			time.Sleep(3 * time.Second)
		}

		// TODO:
		// on some fans it is not possible to use the full pwm of 0..255
		// so we try what values work and save them for later

		// wait a bit to allow the fan speed to settle.
		// since most sensors are update only each second,
		// we wait double that to make sure we get
		// the most recent measurement
		time.Sleep(2 * time.Second)

		log.Printf("Measuring RPM of %s (%s) at PWM: %d", fan.Config.Id, fan.Name, pwm)
		for i := 0; i < CurrentConfig.RpmRollingWindowSize; i++ {
			// update rpm curve
			measureRpm(fan)
		}
	}

	// save to database to restore it on restarts
	err := SaveFanPwmData(db, fan)
	if err != nil {
		log.Fatalf(err.Error())
	}
}

func findFanConfig(controller *Controller, fan *Fan) (fanConfig *FanConfig) {
	for _, fanConfig := range CurrentConfig.Fans {
		if controller.Platform == fanConfig.Platform &&
			fan.Index == fanConfig.Fan {
			return &fanConfig
		}
	}
	return nil
}

func findSensorConfig(controller *Controller, sensor *Sensor) (sensorConfig *SensorConfig) {
	for _, sensorConfig := range CurrentConfig.Sensors {
		if controller.Platform == sensorConfig.Platform &&
			sensor.Index == sensorConfig.Index {
			return &sensorConfig
		}
	}
	return nil
}

// calculates optimal fan speeds for all given devices
func setOptimalFanSpeed(fan *Fan) (err error) {
	target := calculateTargetSpeed(fan)
	return setPwm(fan, target)
}

// calculates the target speed for a given device output
func calculateTargetSpeed(fan *Fan) int {
	sensor := SensorMap[fan.Config.Sensor]
	minTemp := sensor.Config.Min * 1000 // degree to milli-degree
	maxTemp := sensor.Config.Max * 1000

	var avgTemp = sensor.MovingAvg

	//log.Printf("Avg temp of %s: %f", sensor.Config.Id, avgTemp)

	if avgTemp >= maxTemp {
		// full throttle if max temp is reached
		return 255
	} else if avgTemp <= minTemp {
		// turn fan off if at/below min temp
		return 0
	}

	ratio := (avgTemp - minTemp) / (maxTemp - minTemp)
	return int(ratio * 255)
}

// FindControllers Finds controllers and fans
func FindControllers() (controllers []*Controller, err error) {
	hwmonDevices := util.FindHwmonDevicePaths()
	i2cDevices := util.FindI2cDevicePaths()
	allDevices := append(hwmonDevices, i2cDevices...)

	platformRegex := regexp.MustCompile(".*/platform/{}/.*")

	for _, devicePath := range allDevices {
		name := util.GetDeviceName(devicePath)
		dType := util.GetDeviceType(devicePath)
		modalias := util.GetDeviceModalias(devicePath)
		platform := platformRegex.FindString(devicePath)
		if len(platform) <= 0 {
			platform = name
		}

		fans := createFans(devicePath)
		sensors := createSensors(devicePath)

		if len(fans) <= 0 && len(sensors) <= 0 {
			continue
		}

		controller := Controller{
			Name:     name,
			DType:    dType,
			Modalias: modalias,
			Platform: platform,
			Path:     devicePath,
			Fans:     fans,
			Sensors:  sensors,
		}
		controllers = append(controllers, &controller)
	}

	return controllers, err
}

// creates fan objects for the given device path
func createFans(devicePath string) []*Fan {
	var fans []*Fan

	inputs := util.FindFilesMatching(devicePath, "^fan[1-9]_input$")
	outputs := util.FindFilesMatching(devicePath, "^pwm[1-9]$")

	for idx, output := range outputs {
		_, file := filepath.Split(output)

		label := util.GetLabel(devicePath, output)

		index, err := strconv.Atoi(file[len(file)-1:])
		if err != nil {
			log.Fatal(err)
		}

		fan := &Fan{
			Name:         file,
			Label:        label,
			Index:        index,
			PwmOutput:    output,
			RpmInput:     inputs[idx],
			RpmMovingAvg: 0,
			StartPwm:     MinPwmValue,
			MaxPwm:       MaxPwmValue,
			FanCurveData: &map[int]*rolling.PointPolicy{},
			LastSetPwm:   InitialLastSetPwm,
		}

		// store original pwm_enabled value
		pwmEnabled, err := getPwmEnabled(fan)
		if err != nil {
			log.Fatalf("Cannot read pwm_enabled value of %s", fan.Config.Id)
		}
		fan.OriginalPwmEnabled = pwmEnabled

		fans = append(fans, fan)
	}

	return fans
}

// creates sensor objects for the given device path
func createSensors(devicePath string) []*Sensor {
	var sensors []*Sensor

	inputs := util.FindFilesMatching(devicePath, "^temp[1-9]_input$")

	for _, input := range inputs {
		_, file := filepath.Split(input)
		label := util.GetLabel(devicePath, file)

		index, err := strconv.Atoi(string(file[4]))
		if err != nil {
			log.Fatal(err)
		}

		sensors = append(sensors, &Sensor{
			Name:  file,
			Label: label,
			Index: index,
			Input: input,
		})
	}

	return sensors
}

// IsPwmAuto checks if the given output is in auto mode
func IsPwmAuto(outputPath string) (bool, error) {
	pwmEnabledFilePath := outputPath + "_enable"

	if _, err := os.Stat(pwmEnabledFilePath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		panic(err)
	}

	value, err := util.ReadIntFromFile(pwmEnabledFilePath)
	if err != nil {
		return false, err
	}
	return value > 1, nil
}

// Writes the given value to pwmX_enable
// Possible values (unsure if these are true for all scenarios):
// 0 - no control (results in max speed)
// 1 - manual pwm control
// 2 - motherboard pwm control
func setPwmEnabled(fan *Fan, value int) (err error) {
	pwmEnabledFilePath := fan.PwmOutput + "_enable"
	err = util.WriteIntToFile(value, pwmEnabledFilePath)
	if err == nil {
		value, err := util.ReadIntFromFile(pwmEnabledFilePath)
		if err != nil || value != value {
			return errors.New(fmt.Sprintf("PWM mode stuck to %d", value))
		}
	}
	return err
}

// get the pwmX_enabled value of a fan
func getPwmEnabled(fan *Fan) (int, error) {
	pwmEnabledFilePath := fan.PwmOutput + "_enable"
	return util.ReadIntFromFile(pwmEnabledFilePath)
}

// get the maximum valid pwm value of a fan
func getMaxPwmValue(fan *Fan) (result int) {
	return fan.MaxPwm
}

// get the minimum valid pwm value of a fan
func getMinPwmValue(fan *Fan) (result int) {
	// if the fan is never supposed to stop,
	// use the lowest pwm value where the fan is still spinning
	if fan.Config.NeverStop {
		return fan.StartPwm
	}

	return MinPwmValue
}

// GetPwm get the pwm speed of a fan (0..255)
func GetPwm(fan *Fan) int {
	value, err := util.ReadIntFromFile(fan.PwmOutput)
	if err != nil {
		return MinPwmValue
	}
	return value
}

// set the pwm speed of a fan to the specified value (0..255)
func setPwm(fan *Fan, pwm int) (err error) {
	// ensure target value is within bounds of possible values
	if pwm > MaxPwmValue {
		pwm = MaxPwmValue
	} else if pwm < MinPwmValue {
		pwm = MinPwmValue
	}

	// map the target value to the possible range of this fan
	maxPwm := getMaxPwmValue(fan)
	minPwm := getMinPwmValue(fan)

	// TODO: this assumes a linear curve, but it might be something else
	target := minPwm + int((float64(pwm)/MaxPwmValue)*(float64(maxPwm)-float64(minPwm)))

	current := GetPwm(fan)
	if fan.LastSetPwm != InitialLastSetPwm && fan.LastSetPwm != current {
		log.Printf("WARNING: PWM of %s was changed by third party! Last set PWM value was: %d but is now: %d",
			fan.Config.Id, fan.LastSetPwm, current)
	}

	// make sure fans never stop by validating the current RPM
	// and adjusting the target PWM value upwards if necessary
	if fan.Config.NeverStop && fan.LastSetPwm == target {
		avgRpm := fan.RpmMovingAvg
		if avgRpm <= 0 {
			if target >= maxPwm {
				log.Printf("CRITICAL: Fan avg. RPM is %f, even at PWM value %d", avgRpm, target)
				return nil
			}
			log.Printf("WARNING: Increasing startPWM of %s from %d to %d, which is supposed to never stop, but RPM is %f", fan.Config.Id, fan.StartPwm, fan.StartPwm+1, avgRpm)
			fan.StartPwm++
			target++

			// set the moving avg to a value > 0 to prevent
			// this increase from happening too fast
			fan.RpmMovingAvg = 1
		}
	}

	if target == current {
		return nil
	}
	log.Printf("Setting %s (%s) to %d (mapped: %d) ...", fan.Config.Id, fan.Name, pwm, target)
	err = util.WriteIntToFile(target, fan.PwmOutput)
	if err == nil {
		fan.LastSetPwm = target
	}
	return err
}

// GetRpm get the rpm value of a fan
func GetRpm(fan *Fan) int {
	value, err := util.ReadIntFromFile(fan.RpmInput)
	if err != nil {
		return 0
	}
	return value
}

// calculates the new moving average, based on an existing average and buffer size
func updateSimpleMovingAvg(oldAvg float64, n int, newValue float64) float64 {
	return oldAvg + (1/float64(n))*(newValue-oldAvg)
}
