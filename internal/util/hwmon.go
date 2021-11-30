package util

import (
	"io/ioutil"
	"strings"
)

// GetDeviceName read the name of a device
func GetDeviceName(devicePath string) string {
	namePath := devicePath + "/name"
	content, _ := ioutil.ReadFile(namePath)
	name := string(content)
	return strings.TrimSpace(name)
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
