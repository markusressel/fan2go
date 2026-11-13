// Package lmsensors discovers hwmon chips and their features the way libsensors (lm-sensors) does.
//
// DetectedChips uses libsensors itself. It is loaded at runtime with dlopen() (see LibraryNames), so fan2go doesn't
// link it and runs on distributions that ship it as libsensors.so.5 (Debian, Ubuntu, Arch Linux) or libsensors.so.4
// (Fedora, RHEL, openSUSE), and without it. SysfsChips reads the same information directly from sysfs, mirroring
// libsensors 3.6 (lib/sysfs.c), for systems without libsensors; settings from sensors3.conf (ignore, compute) don't
// apply there.
package lmsensors

import (
	"errors"
)

var (
	// ErrUnavailable is returned by DetectedChips if libsensors can't be used (see Load).
	ErrUnavailable = errors.New("libsensors is not available")
	// ErrNoCgo is wrapped by the errors of a build without cgo, which can't load libsensors at all.
	ErrNoCgo = errors.New("fan2go was built without cgo")
	// ErrInit is returned by DetectedChips if libsensors was loaded, but can't be initialized, e.g. because
	// ConfigFile has a syntax error or can't be read.
	ErrInit = errors.New("libsensors can't be initialized")
)

// ConfigFile is the only libsensors configuration file fan2go uses. Unlike the sensors command, fan2go doesn't read
// /etc/sensors.conf or /etc/sensors.d/*: ignore statements there would change the detected features and therefore
// the index of hwmon sensors and fans in existing fan2go configs.
const ConfigFile = "/etc/sensors3.conf"

// Bus types (SENSORS_BUS_TYPE_*)
const (
	BusTypeI2c     = 0
	BusTypeIsa     = 1
	BusTypePci     = 2
	BusTypeSpi     = 3
	BusTypeVirtual = 4
	BusTypeAcpi    = 5
	BusTypeHid     = 6
	BusTypeMdio    = 7
	BusTypeScsi    = 8
	BusTypeSdio    = 9
)

// FeatureType is the type of a feature (sensors_feature_type)
type FeatureType int32

const (
	FeatureTypeIn       FeatureType = 0x00
	FeatureTypeFan      FeatureType = 0x01
	FeatureTypeTemp     FeatureType = 0x02
	FeatureTypePower    FeatureType = 0x03
	FeatureTypeEnergy   FeatureType = 0x04
	FeatureTypeCurr     FeatureType = 0x05
	FeatureTypeHumidity FeatureType = 0x06
	// FeatureTypePwm is reported by libsensors >= 3.6.1 only
	FeatureTypePwm        FeatureType = 0x07
	FeatureTypeVid        FeatureType = 0x10
	FeatureTypeIntrusion  FeatureType = 0x11
	FeatureTypeBeepEnable FeatureType = 0x18
	FeatureTypeFreq       FeatureType = 0x19
)

// SubFeatureType is the type of a subfeature (sensors_subfeature_type): the feature type in the high byte
type SubFeatureType int32

const (
	SubFeatureTypeFanInput  SubFeatureType = 0x100
	SubFeatureTypeFanMin    SubFeatureType = 0x101
	SubFeatureTypeFanMax    SubFeatureType = 0x102
	SubFeatureTypeTempInput SubFeatureType = 0x200
	SubFeatureTypeTempMax   SubFeatureType = 0x201
	SubFeatureTypeTempMin   SubFeatureType = 0x203
	// SubFeatureTypePwmEnable (pwmN_enable) is reported by libsensors >= 3.6.1 only
	SubFeatureTypePwmEnable SubFeatureType = 0x780
)

// Bus identifies the bus of a chip (sensors_bus_id)
type Bus struct {
	Type int16
	Nr   int16
}

// Chip is a detected hwmon chip (sensors_chip_name) with its features
type Chip struct {
	Prefix string
	Bus    Bus
	Addr   int32
	// Path is the sysfs directory with the attribute files of the chip
	Path     string
	Features []Feature
}

// Feature is a main feature of a chip, e.g. "temp1" (sensors_feature)
type Feature struct {
	Name        string
	Number      int32
	Type        FeatureType
	SubFeatures []SubFeature
}

// SubFeature is a single attribute of a feature, e.g. "temp1_input" (sensors_subfeature)
type SubFeature struct {
	Name   string
	Number int32
	Type   SubFeatureType
	Flags  uint32
	// Value is the scaled value (e.g. degrees Celsius for temperatures), read during detection if requested
	// (see WantValue). 0 if it wasn't requested or couldn't be read.
	Value float64
}

// WantValue selects the subfeatures whose values are read during detection.
// Reading every attribute can be slow for some drivers, so only the needed ones are read.
type WantValue func(SubFeatureType) bool

// GetFeatures returns the features of the chip
func (c Chip) GetFeatures() []Feature {
	return c.Features
}

// GetSubFeatures returns the subfeatures of the feature
func (f Feature) GetSubFeatures() []SubFeature {
	return f.SubFeatures
}

// GetValue returns the value read during detection
func (s SubFeature) GetValue() float64 {
	return s.Value
}

// typeScaling returns the divisor that converts the raw sysfs value of a subfeature into its unit
// (get_type_scaling in libsensors 3.6.2)
func typeScaling(t SubFeatureType) float64 {
	switch t {
	case 0x380, 0x1000, 0x286: // power average_interval, vid, temp offset
		return 1000
	case 0x700: // pwm
		return 2.55
	}
	switch t & 0xFF80 {
	case 0x000, 0x200, 0x500, 0x600: // in, temp, curr, humidity (regular subfeatures)
		return 1000
	case 0x300, 0x400: // power, energy (regular subfeatures)
		return 1000000
	}
	return 1
}
