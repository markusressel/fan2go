package nvidia

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/nvidia_base"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
)

type NvidiaController struct {
	Identifier string // e.g. "nvidia-10de2489-0400"
	Name       string // e.g. "NVIDIA GeForce RTX 3060 Ti"

	Fans []fans.NvidiaFan
	// at least currently nvml only supports one temperature sensor
	// (pointer in case no sensor was found at all)
	Sensors []*sensors.NvidiaSensor
}

func GetDevices() []*NvidiaController {
	// FIXME: refactor this to use nvidia_base, somehow..
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

	for i := 0; i < nvDevCount; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			ui.Warning("Couldn't get Handle for NVIDIA device with index %d: %s", i, nvml.ErrorString(ret))
			continue
		}
		devID := nvidia_base.GetDeviceID(device)
		if len(devID) == 0 {
			continue
		}
		name, ret := device.GetName()
		if ret != nvml.SUCCESS {
			name = "N/A"
		}

		var fanSlice = []fans.NvidiaFan{}
		numFans, ret := device.GetNumFans()
		if ret == nvml.SUCCESS && numFans > 0 {
			for fanIdx := 0; fanIdx < numFans; fanIdx++ {
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
				fan.Init()

				fanSlice = append(fanSlice, fan)
			}
		}
		_, ret = device.GetTemperature(nvml.TEMPERATURE_GPU)
		var sensorSlice = []*sensors.NvidiaSensor{}
		if ret == nvml.SUCCESS {
			sensor := &sensors.NvidiaSensor{
				Config: configuration.SensorConfig{
					ID: "N/A",
					Nvidia: &configuration.NvidiaSensorConfig{
						Device: devID,
						Index:  1,
					},
				},
				Label: "Temperature", // (currently?) nvml exposes only one temperature sensor
				Index: 1,             // 1-based index, like HwMon
			}
			sensor.Init()
			sensorSlice = append(sensorSlice, sensor)
		}

		c := &NvidiaController{
			Identifier: devID,
			Name:       name,
			Fans:       fanSlice,
			Sensors:    sensorSlice,
		}
		list = append(list, c)
	}

	return list
}
