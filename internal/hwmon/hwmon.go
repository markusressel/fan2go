package hwmon

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/ui"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/md14454/gosensors"
)

const (
	BusTypeI2c     = 0
	BusTypeIsa     = 1
	BusTypePci     = 2
	BusTypeSpi     = 3
	BusTypeVirtual = 4
	BusTypeAcpi    = 5
	BusTypeHid     = 6
	BusTypeMdio    = 7
	BusTypeScsi    = 8
)

type HwMonController struct {
	Name     string
	DType    string
	Modalias string
	Platform string
	Path     string

	// Fans (can be matched either by enumeration index or channel number)
	Fans []fans.HwMonFan
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

		fanSlice := GetFans(chip)
		sensorMap := GetTempSensors(chip)

		if len(fanSlice) <= 0 && len(sensorMap) <= 0 {
			continue
		}

		c := &HwMonController{
			Name:     identifier,
			DType:    dType,
			Modalias: modalias,
			Platform: platform,
			Path:     chip.Path,
			Fans:     fanSlice,
			Sensors:  sensorMap,
		}
		list = append(list, c)
	}

	return list
}

// getDeviceName read the name of a device
func getDeviceName(devicePath string) string {
	namePath := path.Join(devicePath, "name")
	content, _ := os.ReadFile(namePath)
	name := string(content)
	return strings.TrimSpace(name)
}

// getDeviceModalias read the modalias of a device
func getDeviceModalias(devicePath string) string {
	modaliasPath := path.Join(devicePath, "device", "modalias")
	content, _ := os.ReadFile(modaliasPath)
	return strings.TrimSpace(string(content))
}

// getDeviceType read the type of a device
func getDeviceType(devicePath string) string {
	modaliasPath := path.Join(devicePath, "device", "type")
	content, _ := os.ReadFile(modaliasPath)
	return strings.TrimSpace(string(content))
}

func GetTempSensors(chip gosensors.Chip) map[int]*sensors.HwmonSensor {
	result := map[int]*sensors.HwmonSensor{}

	currentOutputIndex := 0
	features := chip.GetFeatures()
	for j := 0; j < len(features); j++ {
		feature := features[j]

		if !FeatureContainsType(feature, gosensors.FeatureTypeTemp) {
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

			label := getLabel(chip.Path, feature.Name)

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

var (
	FeatureTypePwm        gosensors.FeatureType    = 7
	SubFeatureTypeFanMode gosensors.SubFeatureType = 1920
)

func GetFans(chip gosensors.Chip) []fans.HwMonFan {
	var result = []fans.HwMonFan{}

	features := chip.GetFeatures()
	for j := 0; j < len(features); j++ {
		feature := features[j]

		var pwmChannel = -1
		var rpmChannel = -1
		if feature.Type == FeatureTypePwm {
			_, err := fmt.Sscanf(feature.Name, "pwm%d", &pwmChannel)
			if err != nil {
				ui.Warning("No pwmChannel found for '%s', ignoring.", feature.Name)
				continue
			}
			// assume rpm channel is the same as pwm channel
			rpmChannel = pwmChannel
		} else if feature.Type == gosensors.FeatureTypeFan {
			_, err := fmt.Sscanf(feature.Name, "fan%d", &rpmChannel)
			if err != nil {
				ui.Warning("No rpmChannel found for '%s', ignoring.", feature.Name)
				continue
			}
			// assume pwm channel is the same as rpm channel
			pwmChannel = rpmChannel
		} else {
			ui.Debug("Feature '%s' is not a fan or pwm feature, ignoring.", feature.Name)
			continue
		}

		if pwmChannel == -1 && rpmChannel == -1 {
			ui.Warning("No rpmChannel or pwmChannel found for '%s', ignoring.", feature.Name)
			continue
		}

		subfeatures := feature.GetSubFeatures()

		modeSubFeature := getSubFeature(subfeatures, SubFeatureTypeFanMode)
		if modeSubFeature != nil {
			mode := modeSubFeature.GetValue()
			ui.Debug("Found fan mode %d for feature '%s'", mode, feature.Name)
		}

		rpmAverage := 0.0
		inputSubFeature := getSubFeature(subfeatures, gosensors.SubFeatureTypeFanInput)
		if inputSubFeature != nil {
			rpmAverage = inputSubFeature.GetValue()
		} else {
			rpmChannel = -1
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

		label := getLabel(chip.Path, feature.Name)

		fan := fans.HwMonFan{
			Config: configuration.FanConfig{
				ID:     label,
				MinPwm: &min,
				MaxPwm: &max,
				HwMon: &configuration.HwMonFanConfig{
					Index:      len(result) + 1,
					RpmChannel: rpmChannel,
					PwmChannel: pwmChannel,
					SysfsPath:  chip.Path,
				},
			},
			Label:        label,
			Index:        len(result) + 1,
			RpmMovingAvg: rpmAverage,
		}
		setFanConfigPaths(fan.Config.HwMon)

		result = append(result, fan)
	}

	return result
}

func FeatureContainsType(feature gosensors.Feature, featureType gosensors.FeatureType) bool {
	return feature.Type&featureType != 0
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

// getLabel read the label of a feature
func getLabel(devicePath string, featureName string) string {
	labelPath := path.Join(devicePath, featureName) + "_label"
	content, _ := os.ReadFile(labelPath)
	label := string(content)
	if len(label) <= 0 {
		return path.Join(path.Base(devicePath), featureName)
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
	case BusTypeI2c:
		identifier = fmt.Sprintf("%s-i2c-%d-%x", name, chip.Bus.Nr, chip.Addr)
	case BusTypeIsa:
		identifier = fmt.Sprintf("%s-isa-%d%03x", name, chip.Bus.Nr, chip.Addr)
	case BusTypePci:
		identifier = fmt.Sprintf("%s-pci-%d%03x", name, chip.Bus.Nr, chip.Addr)
	case BusTypeVirtual:
		identifier = fmt.Sprintf("%s-virtual-%d", name, chip.Bus.Nr)
	case BusTypeAcpi:
		identifier = fmt.Sprintf("%s-acpi-%d", name, chip.Bus.Nr)
	case BusTypeHid:
		identifier = fmt.Sprintf("%s-hid-%d-%x", name, chip.Bus.Nr, chip.Addr)
	case BusTypeScsi:
		identifier = fmt.Sprintf("%s-scsi-%d-%x", name, chip.Bus.Nr, chip.Addr)
	}

	return identifier
}

func findPlatform(devicePath string) string {
	platformRegex := regexp.MustCompile(".*/platform/{}/.*")
	return platformRegex.FindString(devicePath)
}

func UpdateFanConfigFromHwMonControllers(controllers []*HwMonController, config *configuration.FanConfig) error {
	for _, controller := range controllers {
		matched, err := regexp.MatchString("(?i)"+config.HwMon.Platform, controller.Platform)
		if err != nil {
			return fmt.Errorf("failed to match platform regex of %s (%s) against controller platform %s", config.ID, config.HwMon.Platform, controller.Platform)
		}
		if !matched {
			continue
		}
		for _, fan := range controller.Fans {
			controllerConfig := fan.Config.HwMon
			if config.HwMon.Index > 0 && controllerConfig.Index != config.HwMon.Index {
				continue
			}
			if config.HwMon.RpmChannel > 0 && controllerConfig.RpmChannel != config.HwMon.RpmChannel {
				continue
			}
			config.HwMon.Index = controllerConfig.Index
			config.HwMon.RpmChannel = controllerConfig.RpmChannel
			config.HwMon.SysfsPath = controllerConfig.SysfsPath
			if config.HwMon.PwmChannel == 0 {
				config.HwMon.PwmChannel = controllerConfig.PwmChannel
			}
			setFanConfigPaths(config.HwMon)
			return nil
		}
	}
	return fmt.Errorf("no hwmon fan matched fan config: %+v", config)
}

func setFanConfigPaths(config *configuration.HwMonFanConfig) {
	config.RpmInputPath = path.Join(config.SysfsPath, fmt.Sprintf("fan%d_input", config.RpmChannel))
	config.PwmPath = path.Join(config.SysfsPath, fmt.Sprintf("pwm%d", config.PwmChannel))
	config.PwmEnablePath = path.Join(config.SysfsPath, fmt.Sprintf("pwm%d_enable", config.PwmChannel))
}
