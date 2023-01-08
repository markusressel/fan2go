package hwmon

import (
	"errors"
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/md14454/gosensors"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	BusTypeIsa     = 1
	BusTypePci     = 2
	BusTypeVirtual = 4
	BusTypeAcpi    = 5
	BusTypeHid     = 6
	BusTypeScsi    = 8
)

type HwMonController struct {
	Name     string
	DType    string
	Modalias string
	Platform string
	Path     string

	// Fans maps from HwMon index -> HwMonFan instance
	Fans map[int]*fans.HwMonFan
	// Sensors maps from HwMon index -> HwmonSensor instance
	Sensors map[int]*sensors.HwmonSensor
}

func GetChips() []*HwMonController {
	gosensors.Init()
	defer gosensors.Cleanup()
	chips := gosensors.GetDetectedChips()

	var list []*HwMonController

	for i := 0; i < len(chips); i++ {
		chip := chips[i]

		var identifier = computeIdentifier(chip)
		dType := getDeviceType(chip.Path)
		modalias := getDeviceModalias(chip.Path)
		platform := findPlatform(chip.Path)
		if len(platform) <= 0 {
			platform = identifier
		}

		fanMap := GetFans(chip)
		sensorMap := GetTempSensors(chip)

		if len(fanMap) <= 0 && len(sensorMap) <= 0 {
			continue
		}

		c := &HwMonController{
			Name:     identifier,
			DType:    dType,
			Modalias: modalias,
			Platform: platform,
			Path:     chip.Path,
			Fans:     fanMap,
			Sensors:  sensorMap,
		}
		list = append(list, c)
	}

	return list
}

// getDeviceName read the name of a device
func getDeviceName(devicePath string) string {
	namePath := path.Join(devicePath, "name")
	content, _ := ioutil.ReadFile(namePath)
	name := string(content)
	return strings.TrimSpace(name)
}

// getDeviceModalias read the modalias of a device
func getDeviceModalias(devicePath string) string {
	modaliasPath := path.Join(devicePath, "device", "modalias")
	content, _ := ioutil.ReadFile(modaliasPath)
	return strings.TrimSpace(string(content))
}

// getDeviceType read the type of a device
func getDeviceType(devicePath string) string {
	modaliasPath := path.Join(devicePath, "device", "type")
	content, _ := ioutil.ReadFile(modaliasPath)
	return strings.TrimSpace(string(content))
}

func GetTempSensors(chip gosensors.Chip) map[int]*sensors.HwmonSensor {
	result := map[int]*sensors.HwmonSensor{}

	currentOutputIndex := 0
	features := chip.GetFeatures()
	for j := 0; j < len(features); j++ {
		feature := features[j]

		if feature.Type != gosensors.FeatureTypeTemp {
			continue
		}

		subfeatures := feature.GetSubFeatures()

		if containsSubFeature(subfeatures, gosensors.SubFeatureTypeTempInput) {
			currentOutputIndex++

			inputSubFeature := getSubFeature(subfeatures, gosensors.SubFeatureTypeTempInput)
			sensorInputPath := path.Join(chip.Path, inputSubFeature.Name)

			max := -1
			if containsSubFeature(subfeatures, gosensors.SubFeatureTypeTempMax) {
				maxSubFeature := getSubFeature(subfeatures, gosensors.SubFeatureTypeTempMax)
				max = int(maxSubFeature.GetValue())
			}

			min := -1
			if containsSubFeature(subfeatures, gosensors.SubFeatureTypeTempMin) {
				minSubFeature := getSubFeature(subfeatures, gosensors.SubFeatureTypeTempMin)
				min = int(minSubFeature.GetValue())
			}

			label := getLabel(chip.Path, inputSubFeature.Name)

			result[currentOutputIndex] = &sensors.HwmonSensor{
				Label:     label,
				Index:     currentOutputIndex,
				Input:     sensorInputPath,
				Max:       max,
				Min:       min,
				MovingAvg: inputSubFeature.GetValue(),
			}
		}
	}

	return result
}

func GetFans(chip gosensors.Chip) map[int]*fans.HwMonFan {
	var result = map[int]*fans.HwMonFan{}

	currentOutputIndex := 0
	features := chip.GetFeatures()
	for j := 0; j < len(features); j++ {
		feature := features[j]

		if feature.Type != gosensors.FeatureTypeFan {
			continue
		}

		subfeatures := feature.GetSubFeatures()

		if containsSubFeature(subfeatures, gosensors.SubFeatureTypeFanInput) {
			pwmOutput := path.Join(chip.Path, fmt.Sprintf("pwm%d", currentOutputIndex+1))

			if _, err := os.Stat(pwmOutput); err == nil {
			} else if errors.Is(err, os.ErrNotExist) {
				// path/to/whatever does *not* exist
				pwmOutput = ""
			} else {
				pwmOutput = ""
			}

			currentOutputIndex++

			if len(pwmOutput) <= 0 {
				continue
			}

			rpmInput := ""
			rpmAverage := 0.0
			inputSubFeature := getSubFeature(subfeatures, gosensors.SubFeatureTypeFanInput)
			if inputSubFeature != nil {
				rpmInput = path.Join(chip.Path, inputSubFeature.Name)
				rpmAverage = inputSubFeature.GetValue()
			}

			max := -1
			if containsSubFeature(subfeatures, gosensors.SubFeatureTypeFanMax) {
				maxSubFeature := getSubFeature(subfeatures, gosensors.SubFeatureTypeFanMax)
				max = int(maxSubFeature.GetValue())
			} else {
				max = fans.MaxPwmValue
			}

			min := -1
			if containsSubFeature(subfeatures, gosensors.SubFeatureTypeFanMin) {
				minSubFeature := getSubFeature(subfeatures, gosensors.SubFeatureTypeFanMin)
				min = int(minSubFeature.GetValue())
			} else {
				min = fans.MinPwmValue
			}

			label := getLabel(chip.Path, inputSubFeature.Name)

			fan := &fans.HwMonFan{
				Config: configuration.FanConfig{
					ID:     label,
					MinPwm: &min,
					MaxPwm: &max,
					HwMon: &configuration.HwMonFanConfig{
						Index:     currentOutputIndex,
						PwmOutput: pwmOutput,
						RpmInput:  rpmInput,
					},
				},
				Label:        label,
				Index:        currentOutputIndex,
				RpmMovingAvg: rpmAverage,
			}

			result[currentOutputIndex] = fan
		}
	}

	return result
}

func getSubFeature(subfeatures []gosensors.SubFeature, input gosensors.SubFeatureType) *gosensors.SubFeature {
	for _, a := range subfeatures {
		if a.Type == input {
			return &a
		}
	}
	return nil
}

func containsSubFeature(s []gosensors.SubFeature, e gosensors.SubFeatureType) bool {
	for _, a := range s {
		if a.Type == e {
			return true
		}
	}
	return false
}

// getLabel read the label of a in/output of a device
func getLabel(devicePath string, input string) string {
	labelPath := strings.TrimSuffix(path.Join(devicePath, input), "input") + "label"

	content, _ := ioutil.ReadFile(labelPath)
	label := string(content)
	if len(label) <= 0 {
		_, label = filepath.Split(devicePath)
	}
	return strings.TrimSpace(label)
}

func computeIdentifier(chip gosensors.Chip) (name string) {
	name = chip.Prefix

	devicePath := chip.Path
	if len(name) <= 0 {
		name = getDeviceName(devicePath)
	}

	if len(name) <= 0 {
		_, name = filepath.Split(devicePath)
	}

	identifier := name
	switch chip.Bus.Type {
	case BusTypeIsa:
		identifier = fmt.Sprintf("%s-isa-%d%x", identifier, chip.Bus.Nr, chip.Addr)
	case BusTypePci:
		identifier = fmt.Sprintf("%s-pci-%d%x", identifier, chip.Bus.Nr, chip.Addr)
	case BusTypeVirtual:
		identifier = fmt.Sprintf("%s-virtual-%d", identifier, chip.Bus.Nr)
	case BusTypeAcpi:
		identifier = fmt.Sprintf("%s-acpi-%d", identifier, chip.Bus.Nr)
	case BusTypeHid:
		identifier = fmt.Sprintf("%s-hid-%d-%d", identifier, chip.Bus.Nr, chip.Addr)
	case BusTypeScsi:
		identifier = fmt.Sprintf("%s-scsi-%d-%d", identifier, chip.Bus.Nr, chip.Addr)
	}

	return identifier
}

func findPlatform(devicePath string) string {
	platformRegex := regexp.MustCompile(".*/platform/{}/.*")
	return platformRegex.FindString(devicePath)
}
