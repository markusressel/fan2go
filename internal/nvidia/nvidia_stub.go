//go:build disable_nvml

package nvidia

func GetDevices() []*NvidiaController {
	// fan2go was built without nvml support => return no devices
	return nil
}
