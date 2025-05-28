package nvidia

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
)

type NvidiaController struct { // TODO: why "controller"?
	Identifier string // e.g. "nvidia-10de2489-0400"
	Name       string // e.g. "NVIDIA GeForce RTX 3060 Ti"

	Fans []fans.NvidiaFan
	// at least currently nvml only supports one temperature sensor
	// (pointer in case no sensor was found at all)
	Sensor *sensors.NvidiaSensor
}

// create identifier for nvidia devices (for config), which is like
//
//	nvidia-<PCI vendor ID><PCI device ID>-<PCI address>
//
// where PCI address is calculated in the same way as libsensor PCI device addresses
// example: "nvidia-10de2489-0400"
func getDeviceID(device nvml.Device) string {
	pciInfo, ret := device.GetPciInfo()
	if ret != nvml.SUCCESS {
		// shouldn't really happen - display error?
		return ""
	}
	var pciVendorID uint16 = uint16(pciInfo.PciDeviceId & 0xFFFF)
	var pciDeviceID uint16 = uint16((pciInfo.PciDeviceId >> 16) & 0xFFFF)
	// TODO: if the PCI "function" value is really needed, it could be parsed from pciInfo.BusId, I guess
	//  for now I assume that 0 always works? (on my card function 1 is the soundcard for HDMI audio => not relevant here)
	var devFunction uint32 = 0
	var addr uint32 = (pciInfo.Domain << 16) + (pciInfo.Bus << 8) + (pciInfo.Device << 3) + devFunction
	return fmt.Sprintf("nvidia-%04X%04X-%04X\n", pciVendorID, pciDeviceID, addr)
}

func GetDevices() []*NvidiaController {
	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		return nil
	}
	defer nvml.Shutdown()

	nvDevCount, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS || nvDevCount < 1 {
		return nil
	}

	var list []*NvidiaController

	var defaultFeatures int = (1 << fans.FeaturePwmSensor) | (1 << fans.FeatureControlMode)
	// TODO: could check (with cgo) if nvmlDeviceGetFanSpeedRPM() is available and returns a value
	//   and if so, set (1 << fans.FeatureRpmSensor)

	for i := 0; i < nvDevCount; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			continue
		}
		devID := getDeviceID(device)
		if len(devID) == 0 {
			continue
		}
		name, ret := device.GetName()
		if ret != nvml.SUCCESS {
			name = "???"
		}

		var fanSlice = []fans.NvidiaFan{}
		numFans, ret := device.GetNumFans()
		if ret == nvml.SUCCESS && numFans > 0 {
			for fanIdx := 0; fanIdx < numFans; fanIdx++ {
				var features int = defaultFeatures
				_, ret := device.GetFanControlPolicy_v2(fanIdx)
				if ret != nvml.SUCCESS {
					// at least nvml.ERROR_NOT_SUPPORTED means that
					// this device doesn't support fan control (older than Maxwell)
					features &= ^(1 << fans.FeatureControlMode)
				}
				_, ret = device.GetFanSpeed_v2(fanIdx)
				if ret != nvml.SUCCESS {
					features &= ^(1 << fans.FeaturePwmSensor)
				}
				max := 100
				min := 0
				label := fmt.Sprintf("Fan %d", fanIdx+1)

				fan := fans.NvidiaFan{
					Config: configuration.FanConfig{
						ID:     "N/A",
						MinPwm: &min,
						MaxPwm: &max,
						Nvidia: &configuration.NvidiaFanConfig{
							Device: devID,
							Index:  fanIdx + 1, // 1-based index, like HwMon
						},
					},
					Label: label,
					Index: fanIdx + 1,
				}

				fanSlice = append(fanSlice, fan)
			}
		}
		_, ret = device.GetTemperature(nvml.TEMPERATURE_GPU)
		var sensor *sensors.NvidiaSensor
		if ret == nvml.SUCCESS {
			sensor = &sensors.NvidiaSensor{
				Label: "Temperature", // (currently?) nvml exposes only one temperature sensor
				Index: 1,             // 1-based index, like HwMon
			}
		}

		c := &NvidiaController{
			Identifier: devID,
			Name:       name,
			Fans:       fanSlice,
			Sensor:     sensor,
		}
		list = append(list, c)
	}

	return list
}
