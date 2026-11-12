package hwmon

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/markusressel/fan2go/internal/ui"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon/lmsensors"
	"github.com/markusressel/fan2go/internal/sensors"
)

const (
	BusTypeI2c     = lmsensors.BusTypeI2c
	BusTypeIsa     = lmsensors.BusTypeIsa
	BusTypePci     = lmsensors.BusTypePci
	BusTypeSpi     = lmsensors.BusTypeSpi
	BusTypeVirtual = lmsensors.BusTypeVirtual
	BusTypeAcpi    = lmsensors.BusTypeAcpi
	BusTypeHid     = lmsensors.BusTypeHid
	BusTypeMdio    = lmsensors.BusTypeMdio
	BusTypeScsi    = lmsensors.BusTypeScsi
)

// sysfsRoot is the mount point of sysfs, used when libsensors is not available
const sysfsRoot = "/sys"

var libsensorsWarning, libsensorsInfo sync.Once

// wantValue selects the subfeature values fan2go uses: initial values and limits of temperature and fan inputs
func wantValue(t lmsensors.SubFeatureType) bool {
	switch t {
	case lmsensors.SubFeatureTypeTempInput, lmsensors.SubFeatureTypeTempMax, lmsensors.SubFeatureTypeTempMin,
		lmsensors.SubFeatureTypeFanInput, lmsensors.SubFeatureTypeFanMax, lmsensors.SubFeatureTypeFanMin,
		SubFeatureTypeFanMode:
		return true
	}
	return false
}

// detectChips returns the hwmon chips detected by libsensors, or, if libsensors can't be used, read directly from
// sysfs (with a one-time warning)
func detectChips() []lmsensors.Chip {
	chips, err := lmsensors.DetectedChips(wantValue)
	if err == nil {
		libsensorsInfo.Do(func() {
			name, _ := lmsensors.Load()
			ui.Debug("Detecting hwmon chips with %s (libsensors %s, %s used: %v)", name, lmsensors.Version(),
				lmsensors.ConfigFile, lmsensors.ConfigUsed())
		})
		return chips
	}
	libsensorsWarning.Do(func() {
		hint := " Install libsensors (libsensors5 on Debian/Ubuntu, lm_sensors-libs on Fedora/RHEL, libsensors4 on " +
			"openSUSE, lm_sensors on Arch Linux) to use it."
		if errors.Is(err, lmsensors.ErrNoCgo) {
			hint = ""
		}
		ui.Warning("Detecting hwmon chips directly in sysfs, because libsensors (lm-sensors) can't be used: %v. "+
			"The ignore and compute statements of %s don't apply.%s", err, lmsensors.ConfigFile, hint)
	})
	chips, err = lmsensors.SysfsChips(sysfsRoot, wantValue)
	if err != nil {
		ui.Error("Detecting hwmon chips in sysfs failed: %v", err)
		return nil
	}
	return chips
}

type HwMonController struct {
	Name string
	// HwmonName is the content of the name file of the hwmon device, e.g. "nct6798" (used to look up known quirks)
	HwmonName string
	DType     string
	// Modalias is the content of the device/modalias file of the hwmon device
	Modalias string
	// Platform is the fan2go name of the chip (see computeIdentifier), as shown by `fan2go detect`
	Platform string
	// LmSensorsName is the chip name as printed by libsensors (`sensors`). It differs from Platform
	// for some chips, e.g. PCI devices with an address >= 0x1000 ("nvme-pci-5a00" vs. "nvme-pci-05a00").
	// The platform of a fan or sensor config may match either of them.
	LmSensorsName string
	Path          string

	// Fans (can be matched either by enumeration index or channel number)
	Fans []*fans.HwMonFan
	// Sensors maps from hwmon channel number -> HwmonSensor instance
	Sensors map[int]*sensors.HwmonSensor
}

func GetChips() []*HwMonController {
	chips := detectChips()

	var list []*HwMonController

	for i := 0; i < len(chips); i++ {
		chip := chips[i]

		var identifier = computeIdentifier(chip)
		dType := getDeviceType(chip.Path)
		modalias := getDeviceModalias(chip.Path)

		fanSlice := GetFans(chip)
		sensorMap := GetTempSensors(chip)

		if len(fanSlice) <= 0 && len(sensorMap) <= 0 {
			continue
		}

		c := &HwMonController{
			Name:          identifier,
			HwmonName:     getDeviceName(chip.Path),
			DType:         dType,
			Modalias:      modalias,
			Platform:      identifier,
			LmSensorsName: computeLmSensorsName(chip),
			Path:          chip.Path,
			Fans:          fanSlice,
			Sensors:       sensorMap,
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

func GetTempSensors(chip lmsensors.Chip) map[int]*sensors.HwmonSensor {
	result := map[int]*sensors.HwmonSensor{}

	currentOutputIndex := 0
	features := chip.GetFeatures()
	for j := 0; j < len(features); j++ {
		feature := features[j]

		if !FeatureContainsType(feature, lmsensors.FeatureTypeTemp) {
			continue
		}

		var channel int
		_, err := fmt.Sscanf(feature.Name, "temp%d", &channel)
		if err != nil {
			ui.Warning("No channel found for '%s', ignoring.", feature.Name)
			continue
		}

		subfeatures := feature.GetSubFeatures()

		if containsSubFeature(subfeatures, lmsensors.SubFeatureTypeTempInput) {
			currentOutputIndex++

			inputSubFeature := getSubFeature(subfeatures, lmsensors.SubFeatureTypeTempInput)
			sensorInputPath := path.Join(chip.Path, inputSubFeature.Name)

			max := -1
			if containsSubFeature(subfeatures, lmsensors.SubFeatureTypeTempMax) {
				maxSubFeature := getSubFeature(subfeatures, lmsensors.SubFeatureTypeTempMax)
				max = int(maxSubFeature.GetValue())
			}

			min := -1
			if containsSubFeature(subfeatures, lmsensors.SubFeatureTypeTempMin) {
				minSubFeature := getSubFeature(subfeatures, lmsensors.SubFeatureTypeTempMin)
				min = int(minSubFeature.GetValue())
			}

			label := getLabel(chip.Path, feature.Name)

			result[channel] = &sensors.HwmonSensor{
				Label:     label,
				Index:     currentOutputIndex,
				Channel:   channel,
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
	// FeatureTypePwm and SubFeatureTypeFanMode (pwmN_enable) are only reported by libsensors >= 3.6.1
	FeatureTypePwm        = lmsensors.FeatureTypePwm
	SubFeatureTypeFanMode = lmsensors.SubFeatureTypePwmEnable
)

func GetFans(chip lmsensors.Chip) []*fans.HwMonFan {
	var result = []*fans.HwMonFan{}

	hwmonName := getDeviceName(chip.Path)
	modalias := getDeviceModalias(chip.Path)
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
		} else if feature.Type == lmsensors.FeatureTypeFan {
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
			ui.Debug("Found fan mode %d for feature '%s'", int(mode), feature.Name)
		}

		rpmAverage := 0.0
		inputSubFeature := getSubFeature(subfeatures, lmsensors.SubFeatureTypeFanInput)
		if inputSubFeature != nil {
			rpmAverage = inputSubFeature.GetValue()
		} else {
			rpmChannel = -1
		}

		max := -1
		if containsSubFeature(subfeatures, lmsensors.SubFeatureTypeFanMax) {
			maxSubFeature := getSubFeature(subfeatures, lmsensors.SubFeatureTypeFanMax)
			max = int(maxSubFeature.GetValue())
		} else {
			max = fans.MaxPwmValue
		}

		min := -1
		if containsSubFeature(subfeatures, lmsensors.SubFeatureTypeFanMin) {
			minSubFeature := getSubFeature(subfeatures, lmsensors.SubFeatureTypeFanMin)
			min = int(minSubFeature.GetValue())
		} else {
			min = fans.MinPwmValue
		}

		label := getLabel(chip.Path, feature.Name)

		fan := &fans.HwMonFan{
			Config: configuration.FanConfig{
				ID:     label,
				MinPwm: &min,
				MaxPwm: &max,
				HwMon: &configuration.HwMonFanConfig{
					Index:      len(result) + 1,
					RpmChannel: rpmChannel,
					PwmChannel: pwmChannel,
					SysfsPath:  chip.Path,
					HwmonName:  hwmonName,
					Modalias:   modalias,
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

func FeatureContainsType(feature lmsensors.Feature, featureType lmsensors.FeatureType) bool {
	return feature.Type == featureType
}

func getSubFeature(subfeatures []lmsensors.SubFeature, input lmsensors.SubFeatureType) *lmsensors.SubFeature {
	for _, a := range subfeatures {
		if a.Type == input {
			return &a
		}
	}
	return nil
}

func containsSubFeature(s []lmsensors.SubFeature, e lmsensors.SubFeatureType) bool {
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

func computeIdentifier(chip lmsensors.Chip) (name string) {
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

// computeLmSensorsName returns the chip name as printed by libsensors (`sensors`), using the same formats as
// libsensors' sensors_snprintf_chip_name, or "" for an unknown bus type.
// Unlike computeIdentifier, it doesn't put the bus number in front of ISA and PCI addresses,
// so both differ for addresses >= 0x1000 (e.g. "nvme-pci-5a00" vs. "nvme-pci-05a00").
func computeLmSensorsName(chip lmsensors.Chip) string {
	prefix := chip.Prefix
	if len(prefix) <= 0 {
		return ""
	}
	switch chip.Bus.Type {
	case BusTypeIsa:
		return fmt.Sprintf("%s-isa-%04x", prefix, chip.Addr)
	case BusTypePci:
		return fmt.Sprintf("%s-pci-%04x", prefix, chip.Addr)
	case BusTypeI2c:
		return fmt.Sprintf("%s-i2c-%d-%02x", prefix, chip.Bus.Nr, chip.Addr)
	case BusTypeSpi:
		return fmt.Sprintf("%s-spi-%d-%x", prefix, chip.Bus.Nr, chip.Addr)
	case BusTypeVirtual:
		return fmt.Sprintf("%s-virtual-%x", prefix, chip.Addr)
	case BusTypeAcpi:
		return fmt.Sprintf("%s-acpi-%x", prefix, chip.Addr)
	case BusTypeHid:
		return fmt.Sprintf("%s-hid-%d-%x", prefix, chip.Bus.Nr, chip.Addr)
	case BusTypeMdio:
		return fmt.Sprintf("%s-mdio-%x", prefix, chip.Addr)
	case BusTypeScsi:
		return fmt.Sprintf("%s-scsi-%d-%x", prefix, chip.Bus.Nr, chip.Addr)
	}
	return ""
}

// platformNameKind selects which name of a HwMonController a platform regex is matched against
type platformNameKind int

const (
	platformNameFan2go platformNameKind = iota
	platformNameLmSensors
)

// controllerPlatformName returns the name of the given kind of the controller,
// or "" if there is no such name or it is identical to the fan2go name
func controllerPlatformName(controller *HwMonController, kind platformNameKind) string {
	switch kind {
	case platformNameLmSensors:
		if controller.LmSensorsName == controller.Platform {
			// already checked as the fan2go name
			return ""
		}
		return controller.LmSensorsName
	default:
		return controller.Platform
	}
}

// matchingControllers returns the controllers whose name of the given kind matches the given (case-insensitive)
// platform regex, in their original order
func matchingControllers(controllers []*HwMonController, platform string, kind platformNameKind) ([]*HwMonController, error) {
	platformRegex, err := regexp.Compile("(?i)" + platform)
	if err != nil {
		return nil, err
	}
	var result []*HwMonController
	for _, controller := range controllers {
		name := controllerPlatformName(controller, kind)
		if len(name) <= 0 {
			continue
		}
		if platformRegex.MatchString(name) {
			result = append(result, controller)
		}
	}
	return result, nil
}

// UpdateFanConfigFromHwMonControllers resolves the hwmon fan of the given config and fills in its paths.
// The platform regex of the config is matched against the fan2go names of the controllers first
// and, only if no fan matched that way, against their libsensors names (see HwMonController.LmSensorsName),
// so configs that match a fan2go name keep resolving to the same fan.
func UpdateFanConfigFromHwMonControllers(controllers []*HwMonController, config *configuration.FanConfig) error {
	for _, kind := range []platformNameKind{platformNameFan2go, platformNameLmSensors} {
		matched, err := matchingControllers(controllers, config.HwMon.Platform, kind)
		if err != nil {
			return fmt.Errorf("failed to match platform regex of %s (%s) against the controller platforms: %v", config.ID, config.HwMon.Platform, err)
		}
		if updateFanConfigFromMatchingControllers(matched, config) {
			return nil
		}
	}
	return fmt.Errorf("no hwmon fan matched fan config: %+v", config)
}

func updateFanConfigFromMatchingControllers(controllers []*HwMonController, config *configuration.FanConfig) bool {
	for _, controller := range controllers {
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
			config.HwMon.HwmonName = controllerConfig.HwmonName
			config.HwMon.Modalias = controllerConfig.Modalias
			if config.HwMon.PwmChannel == 0 {
				config.HwMon.PwmChannel = controllerConfig.PwmChannel
			}
			setFanConfigPaths(config.HwMon)
			return true
		}
	}
	return false
}

// UpdateSensorConfigFromHwMonControllers resolves the hwmon sensor of the given config and fills in its input path.
// The platform regex is matched like in UpdateFanConfigFromHwMonControllers.
func UpdateSensorConfigFromHwMonControllers(controllers []*HwMonController, config *configuration.SensorConfig) error {
	_, err := FindSensorForConfig(controllers, config)
	return err
}

// FindSensorForConfig resolves the hwmon sensor of the given config like UpdateSensorConfigFromHwMonControllers,
// and also returns the matched sensor (with its label, min and max as detected).
func FindSensorForConfig(controllers []*HwMonController, config *configuration.SensorConfig) (*sensors.HwmonSensor, error) {
	for _, kind := range []platformNameKind{platformNameFan2go, platformNameLmSensors} {
		matched, err := matchingControllers(controllers, config.HwMon.Platform, kind)
		if err != nil {
			return nil, fmt.Errorf("failed to match platform regex of %s (%s) against the controller platforms: %v", config.ID, config.HwMon.Platform, err)
		}
		if sensor := updateSensorConfigFromMatchingControllers(matched, config); sensor != nil {
			return sensor, nil
		}
	}
	return nil, fmt.Errorf("no hwmon sensor matched sensor config: %+v", config)
}

func updateSensorConfigFromMatchingControllers(controllers []*HwMonController, config *configuration.SensorConfig) *sensors.HwmonSensor {
	for _, controller := range controllers {
		for _, sensor := range controller.Sensors {
			if config.HwMon.Index > 0 && sensor.Index != config.HwMon.Index {
				continue
			}
			if config.HwMon.Channel > 0 && sensor.Channel != config.HwMon.Channel {
				continue
			}
			config.HwMon.Index = sensor.Index
			config.HwMon.Channel = sensor.Channel
			config.HwMon.TempInput = sensor.Input
			return sensor
		}
	}
	return nil
}

func setFanConfigPaths(config *configuration.HwMonFanConfig) {
	config.RpmInputPath = path.Join(config.SysfsPath, fmt.Sprintf("fan%d_input", config.RpmChannel))
	config.PwmPath = path.Join(config.SysfsPath, fmt.Sprintf("pwm%d", config.PwmChannel))
	config.PwmEnablePath = path.Join(config.SysfsPath, fmt.Sprintf("pwm%d_enable", config.PwmChannel))
}
