package nvidia_base

// I'd prefer this to just be in nvidia/nvidia.go, but it can't, because that causes
// a cyclic dependency, as sensors/nvidia.go and fans/nvidia.go need to call nvidia_base.GetDevice()
// but nvidia/nvidia.go needs to import fans/nvidia.go and sensors/nvidia.go for nvidia.GetDevices().
// So GetDevice() now is in this extra package that does not import any other internal code,
// so everyone can import this.

/* // some C code to use a function only available in some libnvidia-ml.so versions
 #include <stddef.h>
 #include <dlfcn.h>
 #include <stdio.h>

 #if 0 // the following is how nvmlDeviceGetFanSpeedRPM() and related types are defined in nvml.h

	// nvmlDeviceGetFanSpeedRPM() was added in nvidia driver v565 but isn't exposed in go-nvml (yet?)
	// support it anyway (if available, to also support older drivers) with a custom C wrapper
	nvmlReturn_t DECLDIR nvmlDeviceGetFanSpeedRPM(nvmlDevice_t device, nvmlFanSpeedInfo_t *fanSpeed);
	// - nvmlReturn_t is an enum => int should work
	// - DECLDIR is only defined to something on windows
	// - typedef struct nvmlDevice_st* nvmlDevice_t; => void* should work
	typedef struct
	{
		unsigned int version; //!< the API version number - nvmlFanSpeedInfo_v1
		unsigned int fan;     //!< the fan index
		unsigned int speed;   //!< OUT: the fan speed in RPM
	} nvmlFanSpeedInfo_v1_t;
	typedef nvmlFanSpeedInfo_v1_t nvmlFanSpeedInfo_t;
	#define nvmlFanSpeedInfo_v1 NVML_STRUCT_VERSION(FanSpeedInfo, 1)
	#define NVML_STRUCT_VERSION(data, ver) (unsigned int)(sizeof(nvml ## data ## _v ## ver ## _t) | \
	                                                 (ver << 24U))
 #endif // 0

 struct myFanSpeedInfo {
	unsigned int version; // nvmlFanSpeedInfo_v1 == NVML_STRUCT_VERSION(FanSpeedInfo, 1)
	unsigned int fan;
	unsigned int speed;
 };

 static int (*getFanSpeedRPMFnPtr)(void*, struct myFanSpeedInfo*) = NULL;

 // returns nvmlReturn_t; device is nvmlDevice_t
 int my_GetFanSpeedRPMimpl(void* device, int fanIdx, int* out_speed) {
	// NVML_STRUCT_VERSION(FanSpeedInfo, 1) for myFanSpeedInfo::version
	const unsigned structVersion = (unsigned)(sizeof(struct myFanSpeedInfo) | 1 << 24U);
	if(getFanSpeedRPMFnPtr == NULL) {
		*out_speed = -1;
		return 13; // NVML_ERROR_FUNCTION_NOT_FOUND == 13
	}
	struct myFanSpeedInfo fanSpeedInfo = { structVersion, fanIdx, 0 };
	int ret = getFanSpeedRPMFnPtr(device, &fanSpeedInfo);
	if(ret == 0) { // NVML_SUCCESS == 0
		*out_speed = fanSpeedInfo.speed;
	} else {
		*out_speed = -1;
	}
	return ret;
 }

 // nvmlReturn_t DECLDIR nvmlDeviceGetHandleByIndex_v2(unsigned int index, nvmlDevice_t *device);
 // - nvmlDevice_t is a pointer to a struct, so void** should work for nvmlDevice_t*
 // - nvmlReturn_t is an enum, so int should work
 static int (*getDevHandleFn)(unsigned int, void**) = NULL;

 // returns a raw nvmlDevice_t handle (C pointer)
 // to be used with my_GetFanSpeedRPMimpl()
 // returns NULL if this failed (really shouldn't happen if nvml.DeviceGetHandleByIndex(i) succeeded)
 void* my_DeviceGetRawHandleByIndex(int index) {
	void* ret = NULL;
	if(getDevHandleFn == NULL) {
		return NULL;
	}
	if(getDevHandleFn(index, &ret) == 0) {
		return ret;
	}
	return NULL;
 }

 void my_InitFunctionPointers() {
	void* dlHandle = dlopen(NULL, RTLD_NOW);
	if(dlHandle != NULL) {
		getDevHandleFn = dlsym(dlHandle, "nvmlDeviceGetHandleByIndex_v2");
		getFanSpeedRPMFnPtr = dlsym(dlHandle, "nvmlDeviceGetFanSpeedRPM");
	}
 }
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/markusressel/fan2go/internal/ui"
)

type RawNvmlDevice unsafe.Pointer

type NvidiaDevice struct {
	Identifier      string
	DeviceHandle    nvml.Device
	RawDeviceHandle RawNvmlDevice // needed for NvmlGetFanSpeedRPM()
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

func GetDevice(identifier string) (nvml.Device, RawNvmlDevice) {
	nh := nvidiaHandler
	if !nh.isInitialized {
		nh.init()
	}
	for _, device := range nh.devices {
		// TODO: use regex or whatever?
		if strings.HasPrefix(device.Identifier, identifier) {
			return device.DeviceHandle, device.RawDeviceHandle
		}
	}
	return nil, nil
}

func NvmlGetFanSpeedRPM(rawDevice RawNvmlDevice, index int) (int, nvml.Return) {
	var speedRPM C.int = 0
	var ret C.int = C.my_GetFanSpeedRPMimpl(unsafe.Pointer(rawDevice), C.int(index), &speedRPM)
	return int(speedRPM), nvml.Return(ret)
}

// as I found no way to get the raw C nvmlDevice_t from nvml.Device
// (for NvmlGetFanSpeedRPM()), I added this slim wrapper around
// nvml's nvmlDeviceGetHandleByIndex_v2()
func getRawDeviceHandleByIndex(index int) RawNvmlDevice {
	rawDev := C.my_DeviceGetRawHandleByIndex(C.int(index))
	return RawNvmlDevice(rawDev)
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

	C.my_InitFunctionPointers()

	for i := 0; i < nvDevCount; i++ {
		device, ret := nvml.DeviceGetHandleByIndex(i)
		if ret != nvml.SUCCESS {
			ui.Warning("Couldn't get Handle for NVIDIA device with index %d: %s", i, nvml.ErrorString(ret))
			continue
		}
		devID := GetDeviceID(device)
		if len(devID) == 0 {
			continue
		}
		rawDev := getRawDeviceHandleByIndex(i)
		if rawDev == nil {
			// this REALLY shouldn't happen, nvml.DeviceGetHandleByIndex()
			// calls the exact same function under the hood
			ui.Warning("Could not get raw nvmlDevice_t Handle for NVIDIA device %s ?!", devID)
		}

		nh.devices = append(nh.devices, NvidiaDevice{
			Identifier:      devID,
			DeviceHandle:    device,
			RawDeviceHandle: rawDev,
		})
	}
}

// to be called at the end of main() - otherwise probably don't use this
func CleanupAtExit() {
	if nvidiaHandler != nil && nvidiaHandler.isInitialized {
		nvidiaHandler.devices = nil
		nvidiaHandler.isInitialized = false
		// ignore error code returned by Shutdown(), can't do anything about it anyway
		_ = nvml.Shutdown()
	}
}
