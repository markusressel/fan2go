package internal

import (
	"errors"
	"fmt"
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/util"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

const (
	MaxPwmValue = 255
	MinPwmValue = 0
)

var (
	Controllers []*Controller
	SensorMap   = map[string]*Sensor{}
)

func Run() {
	// === Detect devices ===
	controllers, err := findControllers()
	if err != nil {
		log.Fatalf("Error detecting devices: %s", err.Error())
	}
	Controllers = controllers
	mapConfigToControllers()

	// === Print detected devices ===
	log.Printf("Detected Devices:")
	printDeviceStatus(Controllers)

	// try to load fans from persistence
	for _, controller := range Controllers {
		for _, fan := range controller.Fans {
			err = LoadFanPwmData(fan)
		}
	}

	// === start sensor monitoring
	// TODO: use multiple monitoring threads(?)
	// TODO: only monitor configured sensors
	go monitor()

	// wait a bit to gather monitoring data
	time.Sleep(2 * time.Second)

	// === start fan controllers
	// run one goroutine for each fan
	count := 0
	for _, controller := range Controllers {
		for _, fan := range controller.Fans {
			if fan.Config == nil {
				// this fan is not configured, ignore it
				log.Printf("Ignoring unconfigured fan: %s", fan.PwmOutput)
				continue
			}

			go fanController(fan)
			count++
		}
	}

	if count == 0 {
		log.Fatal("No valid fan configurations, exiting.")
	}

	// wait forever
	select {}
}

// Map detect devices to configuration values
func mapConfigToControllers() {
	for _, controller := range Controllers {
		// match fan and fan config entries
		for _, fan := range controller.Fans {
			fanConfig := findFanConfig(controller, fan)
			if fanConfig != nil {
				fan.Config = fanConfig
			}
		}
		// match sensor and sensor config entries
		for _, sensor := range controller.Sensors {
			sensorConfig := findSensorConfig(controller, sensor)
			if sensorConfig != nil {
				sensor.Config = sensorConfig

				SensorMap[sensorConfig.Id] = sensor
				// initialize arrays for storing temps
				pointWindow := rolling.NewPointPolicy(rolling.NewWindow(CurrentConfig.TempRollingWindowSize))
				sensor.Values = pointWindow
				currentValue, err := util.ReadIntFromFile(sensor.Input)
				if err != nil {
					currentValue = 50000
				}
				for i := 0; i < CurrentConfig.TempRollingWindowSize; i++ {
					pointWindow.Append(float64(currentValue))
				}
			}
		}
	}
}

// goroutine to monitor temp and fan sensors
func monitor() {
	go startSensorWatcher()

	// TODO: use realtime measurements to update the curve?

	// TODO: seems like its not possible to watch for changes on temp and rpm files using inotify :(
	//for _, device := range Controllers {
	//	for _, fan := range device.Fans {
	//		watcher, err := startFanFsWatcher(*fan)
	//		if err != nil {
	//			log.Print(err.Error())
	//		} else {
	//			defer watcher.Close()
	//		}
	//	}
	//}

	// wait forever
	select {}
}

//func startFanFsWatcher(fan *data.Fan) (*fsnotify.Watcher, error) {
//	watcher, err := fsnotify.NewWatcher()
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	go func() {
//		for {
//			select {
//			case event, ok := <-watcher.Events:
//				if !ok {
//					return
//				}
//				if event.Op&fsnotify.Write == fsnotify.Write {
//					err := updateFan(fan)
//					if err != nil {
//						log.Print(err.Error())
//					}
//					key := fmt.Sprintf("%s_pwm", fan.Name)
//					newValue, _ := persistence.ReadInt(BucketFans, key)
//					log.Printf("%s PWM: %d", fan.Name, newValue)
//				}
//			case err, ok := <-watcher.Errors:
//				if !ok {
//					return
//				}
//				log.Println("error:", err)
//			}
//		}
//	}()
//
//	err = watcher.Add(fan.RpmInput)
//	err = watcher.Add(fan.PwmOutput)
//	if err != nil {
//		log.Fatal(err.Error())
//	}
//
//	return watcher, err
//}

func startSensorWatcher() {
	// update RPM and Temps at different rates
	tempTick := time.Tick(CurrentConfig.TempSensorPollingRate)
	rpmTick := time.Tick(CurrentConfig.RpmPollingRate)
	for {
		select {
		case <-tempTick:
			measureTempSensors()
		case <-rpmTick:
			measureRpmSensors()
		}
	}
}

func measureRpmSensors() {
	for _, controller := range Controllers {
		for _, fan := range controller.Fans {
			measureRpm(fan)
		}
	}
}

// read the current value of a fan RPM sensor and append it to the moving window
func measureRpm(fan *Fan) {
	pwm := getPwm(fan)
	rpm := getRpm(fan)

	pwmRpmMap := fan.FanCurveData
	if pwmRpmMap == nil {
		// create map for the current fan
		pwmRpmMap = &map[int]*rolling.PointPolicy{}
		fan.FanCurveData = pwmRpmMap
	}
	pointWindow, ok := (*pwmRpmMap)[pwm]
	if !ok {
		// create rolling window for current pwm value
		pointWindow = rolling.NewPointPolicy(rolling.NewWindow(CurrentConfig.RpmRollingWindowSize))
		(*pwmRpmMap)[pwm] = pointWindow
	}
	pointWindow.Append(float64(rpm))
}

func measureTempSensors() {
	for _, controller := range Controllers {
		for _, sensor := range controller.Sensors {
			if sensor.Config != nil {
				err := updateSensor(*sensor)
				if err != nil {
					log.Fatal(err)
				}
			}
		}
	}
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

	if fan.StartPwm != startPwm {
		log.Printf("Start PWM of %s: %d", fan.RpmInput, startPwm)
		fan.StartPwm = startPwm
	}
	if fan.MaxPwm != maxPwm {
		log.Printf("Max PWM of %s: %d", fan.RpmInput, startPwm)
		fan.MaxPwm = maxPwm
	}
}

// read the current value of a sensor and append it to the moving window
func updateSensor(sensor Sensor) (err error) {
	value, err := util.ReadIntFromFile(sensor.Input)
	if err != nil {
		return err
	}

	values := sensor.Values
	values.Append(float64(value))
	if value > sensor.Config.Max {
		// if the value is higher than the specified max temperature,
		// insert the value twice into the moving window,
		// to give it a bigger impact
		values.Append(float64(value))
	}

	return nil
}

// goroutine to continuously adjust the speed of a fan
func fanController(fan *Fan) {
	err := trySetManualPwm(fan)
	if err != nil {
		log.Printf("Could not enable fan control on %s", fan.Name)
		return
	}

	// check if we have data for this fan in persistence,
	// if not we need to run the initialization sequence
	err = LoadFanPwmData(fan)
	if err != nil {
		runInitializationSequence(fan)
	}
	updatePwmBoundaries(fan)

	t := time.Tick(CurrentConfig.ControllerAdjustmentTickRate)
	for {
		select {
		case <-t:
			err = setOptimalFanSpeed(fan)
			if err != nil {
				log.Printf("Error setting %s/%d: %s", fan.Name, fan.Index, err.Error())
				err = trySetManualPwm(fan)
				if err != nil {
					log.Printf("Could not enable fan control on %s", fan.Name)
					return
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
func runInitializationSequence(fan *Fan) {
	log.Printf("Running initialization sequence for %s", fan.Config.Id)
	for pwm := 0; pwm < MaxPwmValue; pwm++ {
		// set a pwm
		err := util.WriteIntToFile(pwm, fan.PwmOutput)
		if err != nil {
			log.Fatalf("Unable to run initialization sequence on %s: %s", fan.Config.Id, err.Error())
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
		// we wait a second + a bit, to make sure we get
		// the most recent measurement
		time.Sleep(1100 * time.Millisecond)

		log.Printf("Measuring RPM of  %s at PWM: %d", fan.Config.Id, pwm)
		for i := 0; i < CurrentConfig.RpmRollingWindowSize; i++ {
			// update rpm curve
			measureRpm(fan)
		}
	}

	// save to database to restore it on restarts
	err := SaveFanPwmData(fan)
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

	var avgTemp int
	avgTemp = int(sensor.Values.Reduce(rolling.Avg))

	//log.Printf("Avg temp of %s: %d", sensor.name, avgTemp)

	if avgTemp >= maxTemp {
		// full throttle if max temp is reached
		return 255
	} else if avgTemp <= minTemp {
		// turn fan off if at/below min temp
		return 0
	}

	ratio := (float64(avgTemp) - float64(minTemp)) / (float64(maxTemp) - float64(minTemp))
	return int(ratio * 255)

	// Toggling between off and "full on" for testing
	//pwm := getPwm(fan)
	//if pwm < 255 {
	//	return 255
	//}
	//
	//return 1

	//return rand.Intn(getMaxPwmValue(fan))
}

// Finds controllers and fans
func findControllers() (controllers []*Controller, err error) {
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

		index, err := strconv.Atoi(file[len(file)-1:])
		if err != nil {
			log.Fatal(err)
		}

		fans = append(fans, &Fan{
			Name:         file,
			Index:        index,
			PwmOutput:    output,
			RpmInput:     inputs[idx],
			StartPwm:     MinPwmValue,
			MaxPwm:       MaxPwmValue,
			FanCurveData: &map[int]*rolling.PointPolicy{},
		})
	}

	return fans
}

// creates sensor objects for the given device path
func createSensors(devicePath string) []*Sensor {
	var sensors []*Sensor

	inputs := util.FindFilesMatching(devicePath, "^temp[1-9]_input$")

	for _, input := range inputs {
		_, file := filepath.Split(input)

		index, err := strconv.Atoi(string(file[4]))
		if err != nil {
			log.Fatal(err)
		}

		sensors = append(sensors, &Sensor{
			Name:  file,
			Index: index,
			Input: input,
		})
	}

	return sensors
}

// checks if the given output is in auto mode
func isPwmAuto(outputPath string) (bool, error) {
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

// get the pwm speed of a fan (0..255)
func getPwm(fan *Fan) int {
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

	current := getPwm(fan)
	if target == current {
		return nil
	}
	log.Printf("Setting %s to %d (mapped: %d) ...", fan.Name, pwm, target)
	return util.WriteIntToFile(target, fan.PwmOutput)
}

// get the rpm value of a fan
func getRpm(fan *Fan) int {
	value, err := util.ReadIntFromFile(fan.RpmInput)
	if err != nil {
		return 0
	}
	return value
}

// ===== Console Output =====

func printDeviceStatus(devices []*Controller) {
	for _, device := range devices {
		log.Printf("Controller: %s", device.Name)
		for _, fan := range device.Fans {
			pwm := getPwm(fan)
			rpm := getRpm(fan)
			isAuto, _ := isPwmAuto(device.Path)
			log.Printf("Fan %d (%s): RPM: %d PWM: %d Auto: %v", fan.Index, fan.Name, rpm, pwm, isAuto)
		}

		for _, sensor := range device.Sensors {
			value, _ := util.ReadIntFromFile(sensor.Input)
			log.Printf("Sensor %d (%s): %d", sensor.Index, sensor.Name, value)
		}
	}
}
