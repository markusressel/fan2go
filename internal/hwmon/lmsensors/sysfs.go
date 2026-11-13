package lmsensors

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// subfeature names per main feature prefix and their types, as in libsensors 3.6.2 (lib/sysfs.c)
var subFeatureTypes = map[string]map[string]SubFeatureType{
	"temp": {
		"input": 0x200, "max": 0x201, "max_hyst": 0x202, "min": 0x203, "crit": 0x204, "crit_hyst": 0x205,
		"lcrit": 0x206, "emergency": 0x207, "emergency_hyst": 0x208, "lowest": 0x209, "highest": 0x20a,
		"min_hyst": 0x20b, "lcrit_hyst": 0x20c,
		"alarm": 0x280, "max_alarm": 0x281, "min_alarm": 0x282, "crit_alarm": 0x283, "fault": 0x284, "type": 0x285,
		"offset": 0x286, "beep": 0x287, "emergency_alarm": 0x288, "lcrit_alarm": 0x289,
	},
	"in": {
		"input": 0x000, "min": 0x001, "max": 0x002, "lcrit": 0x003, "crit": 0x004, "average": 0x005, "lowest": 0x006,
		"highest": 0x007, "rated_min": 0x008, "rated_max": 0x009,
		"alarm": 0x080, "min_alarm": 0x081, "max_alarm": 0x082, "beep": 0x083, "lcrit_alarm": 0x084,
		"crit_alarm": 0x085,
	},
	"fan": {
		"input": 0x100, "min": 0x101, "max": 0x102,
		"alarm": 0x180, "fault": 0x181, "div": 0x182, "beep": 0x183, "pulses": 0x184, "min_alarm": 0x185,
		"max_alarm": 0x186,
	},
	"cpu": {"vid": 0x1000},
	"power": {
		"average": 0x300, "average_highest": 0x301, "average_lowest": 0x302, "input": 0x303, "input_highest": 0x304,
		"input_lowest": 0x305, "cap": 0x306, "cap_hyst": 0x307, "max": 0x308, "crit": 0x309, "min": 0x30a,
		"lcrit": 0x30b, "rated_min": 0x30c, "rated_max": 0x30d,
		"average_interval": 0x380, "alarm": 0x381, "cap_alarm": 0x382, "max_alarm": 0x383, "crit_alarm": 0x384,
		"min_alarm": 0x385, "lcrit_alarm": 0x386,
	},
	"curr": {
		"input": 0x500, "min": 0x501, "max": 0x502, "lcrit": 0x503, "crit": 0x504, "average": 0x505, "lowest": 0x506,
		"highest": 0x507, "rated_min": 0x508, "rated_max": 0x509,
		"alarm": 0x580, "min_alarm": 0x581, "max_alarm": 0x582, "beep": 0x583, "lcrit_alarm": 0x584,
		"crit_alarm": 0x585,
	},
	"energy":    {"input": 0x400},
	"intrusion": {"alarm": 0x1100, "beep": 0x1101},
	"humidity":  {"input": 0x600},
	"freq":      {"input": 0x1900},
	"pwm":       {"freq": 0x701, "enable": 0x780, "mode": 0x781},
}

const (
	subFeatureTypeBeepEnable SubFeatureType = 0x1800
	subFeatureTypePwmIo      SubFeatureType = 0x700
)

var (
	subFeatureNameRegex = regexp.MustCompile(`^(temp|in|fan|cpu|power|curr|energy|intrusion|humidity|freq|pwm)(\d+)_(.+)$`)
	pwmIoNameRegex      = regexp.MustCompile(`^pwm(\d+)$`)
)

// parseSubFeatureName returns the type and channel number of a sysfs attribute name like libsensors'
// sensors_subfeature_get_type (the channel number is 0-based for the feature types that count from 1 in sysfs),
// ok is false for attributes libsensors doesn't know.
func parseSubFeatureName(name string) (t SubFeatureType, nr int, ok bool) {
	if name == "beep_enable" {
		return subFeatureTypeBeepEnable, 0, true
	}
	var number string
	if m := pwmIoNameRegex.FindStringSubmatch(name); m != nil {
		t, number = subFeatureTypePwmIo, m[1]
	} else if m := subFeatureNameRegex.FindStringSubmatch(name); m != nil {
		t, ok = subFeatureTypes[m[1]][m[3]]
		if !ok {
			return 0, 0, false
		}
		number = m[2]
	} else {
		return 0, 0, false
	}
	nr, err := strconv.Atoi(number)
	if err != nil {
		return 0, 0, false
	}
	switch FeatureType(t >> 8) {
	case FeatureTypeFan, FeatureTypeTemp, FeatureTypePower, FeatureTypeEnergy, FeatureTypeCurr, FeatureTypeHumidity,
		FeatureTypePwm:
		nr--
	}
	if nr < 0 || nr >= 1024 {
		return 0, 0, false
	}
	return t, nr, true
}

// SysfsChips detects the hwmon chips below sysfsRoot (normally "/sys") without libsensors, the same way libsensors
// 3.6 does without a configuration file: same chips (in directory order of <sysfsRoot>/class/hwmon), bus types,
// addresses, features and subfeatures. Values of the subfeatures selected by want are read from sysfs and scaled
// like libsensors does.
func SysfsChips(sysfsRoot string, want WantValue) ([]Chip, error) {
	classDir := filepath.Join(sysfsRoot, "class", "hwmon")
	dir, err := os.Open(classDir)
	if err != nil {
		return nil, err
	}
	// unsorted, like readdir() in libsensors
	names, err := dir.Readdirnames(-1)
	_ = dir.Close()
	if err != nil {
		return nil, err
	}

	var chips []Chip
	for _, name := range names {
		if strings.HasPrefix(name, ".") {
			continue
		}
		hwmonPath := filepath.Join(classDir, name)
		devPath, err := resolveDeviceLink(filepath.Join(hwmonPath, "device"))
		if err != nil {
			// no device link: virtual device
			if chip, ok := readChip(sysfsRoot, "", "", hwmonPath, want); ok {
				chips = append(chips, chip)
			}
			continue
		}
		devName := filepath.Base(devPath)
		// the attributes are either those of the hwmon class device or of the device itself
		if chip, ok := readChip(sysfsRoot, devPath, devName, hwmonPath, want); ok {
			chips = append(chips, chip)
		} else if chip, ok := readChip(sysfsRoot, devPath, devName, devPath, want); ok {
			chips = append(chips, chip)
		}
	}
	return chips, nil
}

// resolveDeviceLink resolves a "device" link. Like libsensors (realpath_no_symlink), it ignores a "device" entry that
// isn't a symlink (e.g. the device ID attribute of SDIO devices).
func resolveDeviceLink(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", fmt.Errorf("%s is not a symlink", path)
	}
	return filepath.EvalSymlinks(path)
}

// readAttr returns the first line of a sysfs attribute, ok is false if it can't be read
func readAttr(dir string, attr string) (string, bool) {
	f, err := os.Open(filepath.Join(dir, attr))
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	line, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && line == "" {
		return "", false
	}
	return strings.TrimSuffix(line, "\n"), true
}

// readChip reads the chip whose attributes are in attrPath (sensors_read_one_sysfs_chip), ok is false if it has no
// name attribute, an unknown bus type or no subfeatures
func readChip(sysfsRoot string, devPath string, devName string, attrPath string, want WantValue) (Chip, bool) {
	prefix, ok := readAttr(attrPath, "name")
	if !ok {
		return Chip{}, false
	}
	chip := Chip{Prefix: prefix, Path: attrPath}

	virtual := devPath == ""
	if !virtual {
		found, err := findBusType(sysfsRoot, devPath, devName, &chip)
		if err != nil {
			return Chip{}, false
		}
		virtual = !found
	}
	if virtual {
		chip.Bus = Bus{Type: BusTypeVirtual, Nr: 0}
		chip.Addr = 0
	}

	features, err := readFeatures(attrPath, want)
	if err != nil || len(features) == 0 {
		return Chip{}, false
	}
	chip.Features = features
	return chip, true
}

// findBusType sets bus and address of the chip from its device, walking up the device links until a known bus
// type is found (find_bus_type). found is false if there is none (the chip is treated as virtual).
func findBusType(sysfsRoot string, devPath string, devName string, chip *Chip) (found bool, err error) {
	for devPath != "" {
		subsys := ""
		hasSubsys := false
		link, err := os.Readlink(filepath.Join(devPath, "subsystem"))
		if os.IsNotExist(err) {
			// fallback to the "bus" link of old kernels
			link, err = os.Readlink(filepath.Join(devPath, "bus"))
		}
		if err == nil {
			subsys = filepath.Base(link)
			hasSubsys = true
		} else if !os.IsNotExist(err) {
			return false, err
		}
		if classifyDevice(sysfsRoot, devName, subsys, hasSubsys, chip) {
			return true, nil
		}
		next, err := resolveDeviceLink(filepath.Join(devPath, "device"))
		if err != nil {
			return false, nil
		}
		devPath = next
		devName = filepath.Base(next)
	}
	return false, nil
}

// classifyDevice sets bus and address of the chip from the device name and subsystem (classify_device), returns
// false for an unknown device
func classifyDevice(sysfsRoot string, devName string, subsys string, hasSubsys bool, chip *Chip) bool {
	is := func(names ...string) bool {
		if !hasSubsys {
			return true
		}
		for _, n := range names {
			if subsys == n {
				return true
			}
		}
		return false
	}

	var nr, addr, domain, bus, slot, fn int
	if is("i2c") && scan(devName, "%d-%x", &nr, &addr) {
		chip.Bus.Nr = int16(nr)
		chip.Addr = int32(addr)
		if nr == 9191 { // legacy ISA
			chip.Bus = Bus{Type: BusTypeIsa, Nr: 0}
		} else {
			chip.Bus.Type = BusTypeI2c
			adapterDevice := filepath.Join(sysfsRoot, "class", "i2c-adapter", fmt.Sprintf("i2c-%d", nr), "device")
			if adapterName, ok := readAttr(adapterDevice, "name"); ok && strings.HasPrefix(adapterName, "ISA ") {
				chip.Bus = Bus{Type: BusTypeIsa, Nr: 0}
			}
		}
		return true
	}
	if is("spi") && scan(devName, "spi%d.%d", &nr, &addr) {
		chip.Bus = Bus{Type: BusTypeSpi, Nr: int16(nr)}
		chip.Addr = int32(addr)
		return true
	}
	if is("pci") && scan(devName, "%x:%x:%x.%x", &domain, &bus, &slot, &fn) {
		chip.Addr = int32((domain << 16) + (bus << 8) + (slot << 3) + fn)
		chip.Bus = Bus{Type: BusTypePci, Nr: 0}
		return true
	}
	if is("platform", "of_platform") {
		// must be new ISA (platform driver): "<name>.<addr>" or "<name>:<addr>", else a leading hex number
		chip.Addr = 0
		if m := platformDeviceRegex.FindStringSubmatch(devName); m != nil {
			if a, err := strconv.ParseInt(m[1], 10, 32); err == nil {
				chip.Addr = int32(a)
			}
		} else if m := leadingHexRegex.FindString(devName); m != "" {
			// sscanf("%x.%*s") assigns the number even if no "." follows
			if a, err := strconv.ParseUint(m, 16, 32); err == nil {
				chip.Addr = int32(a)
			}
		}
		chip.Bus = Bus{Type: BusTypeIsa, Nr: 0}
		return true
	}
	if !hasSubsys {
		return false
	}
	switch subsys {
	case "acpi":
		chip.Bus = Bus{Type: BusTypeAcpi, Nr: 0}
		chip.Addr = 0
		return true
	case "hid":
		var vendor, product, id int
		if scan(devName, "%x:%x:%x.%x", &bus, &vendor, &product, &id) {
			chip.Bus = Bus{Type: BusTypeHid, Nr: int16(bus)}
			chip.Addr = int32(id)
			return true
		}
	case "mdio_bus":
		// sscanf(dev_name, "%*[^:]:%d", &addr)
		chip.Addr = 0
		if m := mdioDeviceRegex.FindStringSubmatch(devName); m != nil {
			if a, err := strconv.ParseInt(m[1], 10, 32); err == nil {
				chip.Addr = int32(a)
			}
		}
		chip.Bus = Bus{Type: BusTypeMdio, Nr: 0}
		return true
	case "sdio":
		var host, slot, fn int
		if scan(devName, "mmc%x:%x:%x", &host, &slot, &fn) {
			// mmc host (bus), address, function
			chip.Addr = int32((slot << 3) + fn)
			chip.Bus = Bus{Type: BusTypeSdio, Nr: int16(host)}
			return true
		}
	case "scsi":
		var lun int
		if scan(devName, "%d:%d:%d:%x", &domain, &bus, &slot, &lun) {
			// adapter(host), channel(bus), id(target), lun
			chip.Addr = int32((bus << 8) + (slot << 4) + lun)
			chip.Bus = Bus{Type: BusTypeScsi, Nr: int16(domain)}
			return true
		}
	}
	return false
}

var (
	// "%*[a-zA-Z0-9_]%*1[.:]%d" in libsensors
	platformDeviceRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+[.:]\s*([-+]?\d+)`)
	// "%x" in libsensors
	leadingHexRegex = regexp.MustCompile(`^\s*(?:0[xX])?[0-9a-fA-F]+`)
	mdioDeviceRegex = regexp.MustCompile(`^[^:]+:\s*([-+]?\d+)`)
)

// scan parses s with format like sscanf and reports whether all values were parsed
func scan(s string, format string, args ...any) bool {
	n, _ := fmt.Sscanf(s, format, args...)
	return n == len(args)
}

type sysfsSubFeature struct {
	name string
	t    SubFeatureType
	nr   int
	mode uint32
}

// readFeatures reads the features and subfeatures of the chip with attributes in dir, sorted like libsensors
// (sensors_read_dynamic_chip): by feature type, channel and subfeature type
func readFeatures(dir string, want WantValue) ([]Feature, error) {
	d, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	// unsorted, like readdir() in libsensors: of two names for the same subfeature (e.g. temp01_input and
	// temp1_input), the first one in directory order wins
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return nil, err
	}

	var subFeatures []sysfsSubFeature
	seen := map[string]bool{}
	for _, entry := range entries {
		// skip directories and symlinks
		if !entry.Type().IsRegular() {
			continue
		}
		t, nr, ok := parseSubFeatureName(entry.Name())
		if !ok {
			continue
		}
		key := fmt.Sprintf("%d/%d", t, nr)
		if seen[key] {
			continue
		}
		seen[key] = true
		subFeatures = append(subFeatures, sysfsSubFeature{
			name: entry.Name(),
			t:    t,
			nr:   nr,
			mode: attrMode(filepath.Join(dir, entry.Name())),
		})
	}

	sort.Slice(subFeatures, func(i, j int) bool {
		a, b := subFeatures[i], subFeatures[j]
		if a.t>>8 != b.t>>8 {
			return a.t>>8 < b.t>>8
		}
		if a.nr != b.nr {
			return a.nr < b.nr
		}
		return sortKey(a.t) < sortKey(b.t)
	})

	var features []Feature
	for i, sf := range subFeatures {
		featureType := FeatureType(sf.t >> 8)
		if i == 0 || subFeatures[i-1].t>>8 != sf.t>>8 || subFeatures[i-1].nr != sf.nr {
			features = append(features, Feature{
				Name:   featureName(featureType, sf.name),
				Number: int32(len(features)),
				Type:   featureType,
			})
		}
		flags := sf.mode
		if sf.t < 0x1000 && sf.t&0x80 == 0 {
			// SENSORS_COMPUTE_MAPPING: affected by compute statements (there are none without libsensors)
			flags |= 4
		}
		subFeature := SubFeature{
			Name:   sf.name,
			Number: int32(i),
			Type:   sf.t,
			Flags:  flags,
		}
		if want != nil && want(sf.t) && sf.mode&1 != 0 {
			if value, ok := readValue(filepath.Join(dir, sf.name)); ok {
				subFeature.Value = value / typeScaling(sf.t)
			}
		}
		last := &features[len(features)-1]
		last.SubFeatures = append(last.SubFeatures, subFeature)
	}
	return features, nil
}

// sortKey orders the subfeatures of a feature like the slots of libsensors' sparse table
func sortKey(t SubFeatureType) int {
	if FeatureType(t>>8) < FeatureTypeVid {
		// first the regular subfeatures, then the alarms/other subfeatures (0x80 bit)
		return int(t&0x80)<<8 | int(t&0x7F)
	}
	return int(t & 0xFF)
}

// featureName returns the name of the main feature of a subfeature, e.g. "temp1" for "temp1_input"
func featureName(t FeatureType, subFeatureName string) string {
	switch t {
	case FeatureTypeIn, FeatureTypeFan, FeatureTypeTemp, FeatureTypePower, FeatureTypeEnergy, FeatureTypeCurr,
		FeatureTypeHumidity, FeatureTypeIntrusion, FeatureTypeFreq:
		if i := strings.Index(subFeatureName, "_"); i >= 0 {
			return subFeatureName[:i]
		}
	case FeatureTypePwm:
		// libsensors uses the first 4 characters, since "pwmN" has no underscore (so pwm10 is named "pwm1" too)
		if len(subFeatureName) > 4 {
			return subFeatureName[:4]
		}
	}
	return subFeatureName
}

// attrMode returns SENSORS_MODE_R (1) and SENSORS_MODE_W (2) from the owner permissions of the attribute file
func attrMode(path string) uint32 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	var mode uint32
	if info.Mode().Perm()&0o400 != 0 {
		mode |= 1
	}
	if info.Mode().Perm()&0o200 != 0 {
		mode |= 2
	}
	return mode
}

// readValue reads the leading number of an attribute file (fscanf "%lf")
func readValue(path string) (float64, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(content))
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
