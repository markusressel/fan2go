/*
 * fan2go
 * Copyright (c) 2019. Markus Ressel
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as
 * published by the Free Software Foundation, either version 3 of the
 * License, or (at ydour option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */
package main

import (
	"errors"
	"fmt"
	"github.com/elliotchance/c2go/util"
	"io/ioutil"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	//"github.com/markusressel/fan2go/cmd"
	"log"
	"os"
	"path/filepath"
)

const (
	MaxPwmValue = 255
)

type Device struct {
	name    string
	path    string
	inputs  []Input
	outputs []Output
}

type Output struct {
	name string
	path string
}

type Input struct {
	name string
	path string
}

func main() {
	// TODO: enable
	//if getProcessOwner() != "root" {
	//	log.Fatalf("Please run fan2go as root")
	//}

	//cmd.Execute()

	// DELAY=5 # 3 seconds delay is too short for large fans, thus I increased it to 5

	//var config = config.CurrentConfig

	findFanDevices()
}

func getProcessOwner() string {
	stdout, err := exec.Command("ps", "-o", "user=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	return strings.TrimSpace(string(stdout))
}

func findFanDevices() (err error) {
	//"/sys/bus/i2c/devices"

	hwmonDevices := findDevicesHwmon()
	i2cDevices := findDevicesI2c()

	allDevices := append(hwmonDevices, i2cDevices...)

	var devices []Device

	for _, devicePath := range allDevices {
		// try to get platform name, as a more unique identifier than the hwmon
		r := regexp.MustCompile(".*/platform/{}/.*")
		platform := r.FindString(devicePath)

		outputPaths := findFanOutputs(devicePath)
		inputPaths := findFanInputs(devicePath)

		var workingOutputs []Output
		for _, outputPath := range outputPaths {
			// try to disable PWM for all outputs
			// TODO: this should only be done for things that are configured
			err = enablePwm(outputPath)
			err = disablePwm(outputPath)
			if err == nil {
				_, file := filepath.Split(outputPath)
				workingOutputs = append(workingOutputs, Output{
					name: file,
					path: outputPath,
				})
			} else {
				log.Printf("Could not disable PWM for %s: %s", outputPath, err.Error())
			}
			enablePwm(outputPath)
		}

		if len(workingOutputs) <= 0 {
			log.Printf("No usable PWM outputs for %s, skipping.", devicePath)
			continue
		}

		var inputs []Input
		for _, inputPath := range inputPaths {
			_, file := filepath.Split(inputPath)
			inputs = append(inputs, Input{
				name: file,
				path: inputPath,
			})
		}

		_, file := filepath.Split(devicePath)
		var name string
		if len(platform) > 0 {
			name = platform
		} else {
			name = file
		}

		device := Device{
			name:    name,
			path:    devicePath,
			inputs:  inputs,
			outputs: workingOutputs,
		}
		devices = append(devices, device)
	}

	printDeviceStatus(devices)
	for _, pwm := range []int{1, 125, 20} {
		for _, device := range devices {
			for _, output := range device.outputs {
				err := setPwm(output, pwm)
				if err != nil {
					log.Printf("Could set PWM for %s: %s", device.path, err.Error())
				}
			}
		}
		time.Sleep(10 * time.Second)
	}
	printDeviceStatus(devices)

	return err
}

func printDeviceStatus(devices []Device) {
	for _, device := range devices {
		fmt.Printf("Device: %s\n", device.name)
		for _, output := range device.outputs {
			currentOutput := readIntFromFile(output.path)
			isAuto := isPwmAuto(device.path)
			fmt.Printf("Output: %s Value: %d Auto: %v\n", output.name, currentOutput, isAuto)
		}
	}
}

func readIntFromFile(path string) int {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Println("File reading error", err)
		return -1
	}
	text := string(data)
	text = strings.TrimSpace(text)
	value := util.Atoi(text)
	return value
}

func findDevicesI2c() []string {
	basePath := "/sys/bus/i2c/devices"

	if _, err := os.Stat(basePath); err != nil {
		if os.IsNotExist(err) {
			// file does not exist
		} else {
			// other error
		}
		return []string{}
	}

	return findFilesMatching(basePath, ".+-.+")

	//	# Find available fan control outputs
	//	MATCH=$device/'pwm[1-9]'
	//	device_pwm=$(echo $MATCH)
	//	if [ "$SYSFS" = "1" -a "$MATCH" = "$device_pwm" ]
	//	then
	//		# Deprecated naming scheme (used in kernels 2.6.5 to 2.6.9)
	//		MATCH=$device/'fan[1-9]_pwm'
	//		device_pwm=$(echo $MATCH)
	//	fi
	//	if [ "$MATCH" != "$device_pwm" ]
	//	then
	//		PWM="$PWM $device_pwm"
	//	fi
}

func findDevicesHwmon() []string {
	basePath := "/sys/class/hwmon"
	if _, err := os.Stat(basePath); err != nil {
		if os.IsNotExist(err) {
			// file does not exist
		} else {
			// other error
		}
		return []string{}
	}

	//result := findFilesMatching(basePath, "hwmon.*")

	var result []string
	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Fatalf(err.Error())
		}

		if !info.IsDir() && strings.HasPrefix(info.Name(), "hwmon") {
			var devicePath string

			// we may need to adjust the path (pwmconfig cite...)
			_, err := os.Stat(path + "/name")
			if os.IsNotExist(err) {
				devicePath = path + "/device"
			} else {
				devicePath = path
			}

			devicePath, err = filepath.EvalSymlinks(devicePath)
			if err != nil {
				panic(err)
			}

			//fmt.Printf("File Name: %s\n", info.Name())
			result = append(result, devicePath)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

	return result
}

func findFilesMatching(path string, regex string) []string {
	r, err := regexp.Compile(regex)
	if err != nil {
		log.Fatalf("Cannot compile regex: %s", regex)
	}

	var result []string
	err = filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Fatalf(err.Error())
		}

		if !info.IsDir() && r.MatchString(info.Name()) {
			var devicePath string

			// we may need to adjust the path (pwmconfig cite...)
			_, err := os.Stat(path + "/name")
			if os.IsNotExist(err) {
				devicePath = path + "/device"
			} else {
				devicePath = path
			}

			devicePath, err = filepath.EvalSymlinks(devicePath)
			if err != nil {
				panic(err)
			}

			//fmt.Printf("File Name: %s\n", info.Name())
			result = append(result, devicePath)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

	return result
}

// Finds available fan monitoring outputs for given device
func findFanOutputs(devicePath string) []string {
	r := regexp.MustCompile("^pwm[1-9]$")

	var result []string
	err := filepath.Walk(devicePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Fatalf(err.Error())
		}

		if !info.IsDir() && r.MatchString(info.Name()) {
			//fmt.Printf("File Name: %s\n", info.Name())
			result = append(result, path)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

	return result
}

// Find available fan monitoring input for given device
func findFanInputs(devicePath string) []string {
	r := regexp.MustCompile("^fan[1-9]_input$")

	var result []string
	err := filepath.Walk(devicePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Fatalf(err.Error())
		}
		if r.MatchString(info.Name()) {
			//fmt.Printf("File Name: %s\n", info.Name())
			result = append(result, path)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

	return result
}

// checks if the given output is in auto mode
func isPwmAuto(outputPath string) bool {
	pwmEnabledFilePath := outputPath + "_enable"

	if _, err := os.Stat(pwmEnabledFilePath); err != nil {
		if os.IsNotExist(err) {
			return false
		}
		panic(err)
	}

	return readIntFromFile(pwmEnabledFilePath) > 1
}

func enablePwm(outputPath string) (err error) {
	pwmEnabledFilePath := outputPath + "_enable"
	err = writeIntToFile(1, pwmEnabledFilePath)
	if err != nil {
		return err
	}
	err = writeIntToFile(MaxPwmValue, outputPath)
	return err
}

func disablePwm(outputPath string) (err error) {
	pwmEnabledFilePath := outputPath + "_enable"

	currentValue := readIntFromFile(outputPath)

	if _, err := os.Stat(pwmEnabledFilePath); err != nil {
		if os.IsNotExist(err) {
			// No enable file? Just set to max
			err = writeIntToFile(MaxPwmValue, pwmEnabledFilePath)
			return err
		}
		panic(err)
	}

	// Try pwmN_enable=0
	err = writeIntToFile(0, pwmEnabledFilePath)
	if err == nil {
		value := readIntFromFile(pwmEnabledFilePath)
		if value == 0 {
			err = writeIntToFile(currentValue/2+1, outputPath)
			if err == nil {
				// success
				return err
			}
		}
	}

	//	# It didn't work, try pwmN_enable=1 pwmN=255
	err = writeIntToFile(1, pwmEnabledFilePath)
	if err == nil {
		value := readIntFromFile(pwmEnabledFilePath)
		if value != 1 {
			return errors.New(fmt.Sprintf("PWM mode stuck to %d", value))
		}
	}

	err = writeIntToFile(getMaxPwmValue(outputPath), outputPath)
	if err == nil {
		time.Sleep(1 * time.Second)
		value := readIntFromFile(outputPath)
		if value >= getMaxPwmValue(outputPath) {
			// success
			return nil
		} else {
			return errors.New(fmt.Sprintf("PWM stuck to %d", value))
		}
	}

	return err
}

func getMaxPwmValue(path string) int {
	// TODO: read from config

	return MaxPwmValue
}

func setPwm(output Output, pwm int) (err error) {
	log.Printf("Setting pwm of %s to %d ...", output.name, pwm)
	return writeIntToFile(pwm, output.path)
}

// write a single integer to a file path
func writeIntToFile(value int, path string) (err error) {
	f, err := os.OpenFile(path, os.O_SYNC|os.O_WRONLY, 644)
	if err != nil {
		return err
	}
	defer f.Close()

	valueAsString := fmt.Sprintf("%d", value)
	_, err = f.WriteString(valueAsString)
	if err != nil {
		//log.Printf(err.Error())
		return err
	}

	return err
}
