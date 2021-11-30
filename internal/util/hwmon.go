package util

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GetDeviceName read the name of a device
func GetDeviceName(devicePath string) string {
	namePath := devicePath + "/name"
	content, _ := ioutil.ReadFile(namePath)
	name := string(content)
	return strings.TrimSpace(name)
}

// GetLabel read the label of a in/output of a device
func GetLabel(devicePath string, input string) string {
	labelPath := strings.TrimSuffix(devicePath+"/"+input, "input") + "label"

	content, _ := ioutil.ReadFile(labelPath)
	label := string(content)
	if len(label) <= 0 {
		_, label = filepath.Split(devicePath)
	}
	return strings.TrimSpace(label)
}

// GetDeviceModalias read the modalias of a device
func GetDeviceModalias(devicePath string) string {
	modaliasPath := devicePath + "/device/modalias"
	content, _ := ioutil.ReadFile(modaliasPath)
	return strings.TrimSpace(string(content))
}

// GetDeviceType read the type of a device
func GetDeviceType(devicePath string) string {
	modaliasPath := devicePath + "/device/type"
	content, _ := ioutil.ReadFile(modaliasPath)
	return strings.TrimSpace(string(content))
}

func FindI2cDevicePaths() []string {
	basePath := "/sys/bus/i2c/devices"

	if _, err := os.Stat(basePath); err != nil {
		if os.IsNotExist(err) {
			// file.go does not exist
		} else {
			// other error
		}
		return []string{}
	}

	regex := regexp.MustCompile(".+-.+")
	return FindFilesMatching(basePath, regex)

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

func FindHwmonDevicePaths() []string {
	basePath := "/sys/class/hwmon"
	if _, err := os.Stat(basePath); err != nil {
		if os.IsNotExist(err) {
			// file.go does not exist
		} else {
			// other error
		}
		return []string{}
	}

	regex := regexp.MustCompile("hwmon.*")
	result := FindFilesMatching(basePath, regex)

	return result
}

func CreateShortPciIdentifier(path string) string {
	splits := strings.Split(path, ":")

	domain := splits[0]
	bus := splits[1]

	splits = strings.Split(splits[2], ".")

	slot, channel := splits[0], splits[1]

	name := fmt.Sprintf(
		"%s%s%s%s",
		HexString(domain),
		HexString(bus),
		HexString(slot),
		HexString(channel),
	)
	name = strings.Trim(name, "-")
	name = fmt.Sprintf("pci-%s", name)
	return name
}
