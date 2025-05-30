package nvidia_base

// I'd prefer this to just be in nvidia/nvidia.go, but it can't, because that causes
// a cyclic dependency, as sensors/nvidia.go and fans/nvidia.go need to call nvidia_base.GetDevice()
// but nvidia/nvidia.go needs to import fans/nvidia.go and sensors/nvidia.go for nvidia.GetDevices().
// So GetDevice() now is in this extra package that does not import any other internal code,
// so everyone can import this.

import (
	"fmt"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/markusressel/fan2go/internal/ui"
)

type NvidiaDevice struct {
	Identifier   string
	DeviceHandle nvml.Device
	// TODO: raw devicehandle for getrpm
}

type nvidiaHandlerImpl struct {
	isInitialized bool
	devices       []NvidiaDevice
}

var nvidiaHandler *nvidiaHandlerImpl = &nvidiaHandlerImpl{
	isInitialized: false,
	devices:       nil,
}

// create identifier for nvidia devices (for config), which is like
//
//	nvidia-<PCI vendor ID><PCI device ID>-<PCI address>
//
// where PCI address is calculated in the same way as libsensor PCI device addresses
// example: "nvidia-10de2489-0400"
func GetDeviceID(device nvml.Device) string {
	pciInfo, ret := device.GetPciInfo()
	if ret != nvml.SUCCESS {
		ui.Warning("Couldn't get PCI Info for NVIDIA device: %s", nvml.ErrorString(ret))
		return ""
	}
	pciVendorID := uint16(pciInfo.PciDeviceId & 0xFFFF)
	pciDeviceID := uint16((pciInfo.PciDeviceId >> 16) & 0xFFFF)
	// NOTE: libsensor PCI adresses also add a PCI "function" to this. At the moment I don't think
	//  that this is needed here (for the GPU it seems to be always 0, though for the HDMI soundcard
	//  integrated in the GPU it's 1, but that's not relevant here), so I leave it out.
	//  If it turns out to be needed after all, it could be parsed from the last part of pciInfo.BusId
	//  (nvml.PciInfo and PciInfoExt have no integer providing this)
	var addr uint32 = (pciInfo.Domain << 16) + (pciInfo.Bus << 8) + (pciInfo.Device << 3)
	return fmt.Sprintf("nvidia-%04X%04X-%04X\n", pciVendorID, pciDeviceID, addr)
}

func GetDevice(identifier string) nvml.Device {
	nh := nvidiaHandler
	if !nh.isInitialized {
		nh.init()
	}
	for _, device := range nh.devices {
		// TODO: use regex or whatever?
		if strings.HasPrefix(device.Identifier, identifier) {
			return device.DeviceHandle
		}
	}
	return nil
}

func (nh *nvidiaHandlerImpl) init() {
	nh.isInitialized = true
	ret := nvml.Init()
	if ret != nvml.SUCCESS {
		return // probably no nvidia driver is installed, doesn't have to be an error
	}

	nvDevCount, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS || nvDevCount < 1 {
		return // not really an error, maybe there just is no nvidia hardware or driver
	}

	for i := 0; i < nvDevCount; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			ui.Warning("Couldn't get Handle for NVIDIA device with index %d: %s", i, nvml.ErrorString(ret))
			continue
		}
		// TODO: raw handle for getrpm, if we have nvmlDeviceGetFanSpeedRPM
		devID := GetDeviceID(device)
		if len(devID) == 0 {
			continue
		}
		nh.devices = append(nh.devices, NvidiaDevice{
			Identifier:   devID,
			DeviceHandle: device,
		})
	}
}

// to be called at the end of main() - otherwise probably don't use this
func CleanupAtExit() {
	if nvidiaHandler != nil && nvidiaHandler.isInitialized {
		nvidiaHandler.devices = nil
		nvidiaHandler.isInitialized = false
		nvml.Shutdown()
	}
}
