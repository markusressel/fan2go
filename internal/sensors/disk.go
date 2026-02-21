package sensors

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
)

const (
	hdioDriverCmd    = 0x031f // ioctl: ATA drive command
	ataOpSmart       = 0xb0   // WIN_SMART ATA command
	smartReadData    = 0xd0   // SMART READ DATA subcommand
	smartAttrAirflow = 190    // SMART attribute: airflow temp
	smartAttrTemp    = 194    // SMART attribute: drive temp
)

type DiskSensor struct {
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`
	mu        sync.Mutex
}

func (s *DiskSensor) GetId() string {
	return s.Config.ID
}

func (s *DiskSensor) GetLabel() string {
	return fmt.Sprintf("Disk (%s)", s.Config.Disk.Device)
}

func (s *DiskSensor) GetConfig() configuration.SensorConfig {
	return s.Config
}

func resolveDevice(device string) (string, error) {
	return resolveDeviceAt(device, "/dev", "/dev/disk/by-id")
}

func resolveDeviceAt(device, devBase, byIdBase string) (string, error) {
	if strings.HasPrefix(device, "/") {
		resolved, err := filepath.EvalSymlinks(device)
		if err != nil {
			return "", fmt.Errorf("failed to resolve device %s: %w", device, err)
		}
		return resolved, nil
	}

	// Relative name: try <devBase>/<device> first (covers "sda", "nvme0n1", "disk/by-id/…")
	devPath := devBase + "/" + device
	if resolved, err := filepath.EvalSymlinks(devPath); err == nil {
		return resolved, nil
	}

	// Fallback: try <byIdBase>/<device> (covers bare IDs like "ata-…", "nvme-…")
	byIdPath := byIdBase + "/" + device
	if resolved, err := filepath.EvalSymlinks(byIdPath); err == nil {
		return resolved, nil
	}

	// Neither worked — return a clear error pointing at the primary candidate
	_, err := filepath.EvalSymlinks(devPath)
	return "", fmt.Errorf("failed to resolve device %s: %w", device, err)
}

func (s *DiskSensor) GetValue() (float64, error) {
	resolved, err := resolveDevice(s.Config.Disk.Device)
	if err != nil {
		return 0, err
	}
	deviceName := filepath.Base(resolved)

	// Primary: sysfs hwmon (drivetemp for SATA, nvme-hwmon for NVMe)
	if temp, err := readDiskTempFromSysfs(deviceName); err == nil {
		return temp, nil
	}

	// Fallback: ATA SMART ioctl (SATA/IDE only)
	return readAtaSmartTemp(resolved)
}

func (s *DiskSensor) GetMovingAvg() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MovingAvg
}

func (s *DiskSensor) SetMovingAvg(avg float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MovingAvg = avg
}

func readDiskTempFromSysfs(deviceName string) (float64, error) {
	return readDiskTempFromSysfsAt("/sys", deviceName)
}

func readDiskTempFromSysfsAt(sysBase, deviceName string) (float64, error) {
	patterns := []string{
		fmt.Sprintf("%s/class/block/%s/device/hwmon/hwmon*/temp*_input", sysBase, deviceName),
	}
	// NVMe: nvme0n1 → nvme0 controller path (strip namespace suffix after "nvme<digits>")
	if strings.HasPrefix(deviceName, "nvme") {
		ctrl := deviceName
		if idx := strings.Index(deviceName[4:], "n"); idx >= 0 {
			ctrl = deviceName[:4+idx]
		}
		patterns = append(patterns,
			fmt.Sprintf("%s/class/nvme/%s/hwmon*/temp*_input", sysBase, ctrl))
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			continue
		}
		path := selectTempInput(matches)
		millidegrees, err := util.ReadIntFromFile(path)
		if err != nil {
			continue
		}
		return float64(millidegrees), nil
	}
	return 0, fmt.Errorf("no sysfs hwmon temperature for %s", deviceName)
}

// selectTempInput prefers temp1_input (composite/device temperature)
func selectTempInput(paths []string) string {
	for _, p := range paths {
		if strings.HasSuffix(p, "temp1_input") {
			return p
		}
	}
	return paths[0]
}

// readAtaSmartTemp reads temperature via HDIO_DRIVE_CMD ioctl (ATA SMART).
// Returns millidegrees Celsius.
func readAtaSmartTemp(device string) (float64, error) {
	f, err := os.Open(device)
	if err != nil {
		return 0, fmt.Errorf("cannot open %s: %w", device, err)
	}
	defer f.Close()

	buf := make([]byte, 4+512)
	buf[0] = ataOpSmart    // ATA command: SMART
	buf[1] = 1             // one sector of data to return
	buf[2] = smartReadData // SMART subcommand: READ DATA
	buf[3] = 0             // sector number (unused for SMART)

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		f.Fd(),
		hdioDriverCmd,
		uintptr(unsafe.Pointer(&buf[0])),
	)
	if errno != 0 {
		return 0, fmt.Errorf("HDIO_DRIVE_CMD ioctl on %s: %w", device, errno)
	}

	return parseAtaSmartAttributes(buf[4:], device)
}

// parseAtaSmartAttributes parses the SMART READ DATA attribute table and
// returns the temperature in millidegrees Celsius.
// data is the 512-byte payload (without the 4-byte ioctl header).
func parseAtaSmartAttributes(data []byte, device string) (float64, error) {
	// SMART READ DATA layout:
	// offset 2: attribute table, 30 entries × 12 bytes
	// each entry: [id(1), flags(2), current(1), worst(1), raw(6), reserved(1)]
	for i := 0; i < 30; i++ {
		off := 2 + i*12
		if off+12 > len(data) {
			break
		}
		id := data[off]
		if id == smartAttrTemp || id == smartAttrAirflow {
			return float64(data[off+5]) * 1000, nil // raw[0] = degrees C
		}
	}
	return 0, fmt.Errorf("no temperature attribute in SMART data for %s", device)
}
