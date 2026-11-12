//go:build cgo

package lmsensors

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// The libsensors types used here, as declared in <sensors/sensors.h>. The ABI is stable across libsensors.so.4 and
// libsensors.so.5 (lm-sensors 3.x), so fan2go doesn't need the libsensors headers to build.
typedef struct { short type; short nr; } f2g_sensors_bus_id;
typedef struct { char *prefix; f2g_sensors_bus_id bus; int addr; char *path; } f2g_sensors_chip_name;
typedef struct { char *name; int number; int type; int first_subfeature; int padding1; } f2g_sensors_feature;
typedef struct { char *name; int number; int type; int mapping; unsigned int flags; } f2g_sensors_subfeature;

static void *f2g_handle = NULL;
static int (*f2g_init)(FILE *) = NULL;
static void (*f2g_cleanup)(void) = NULL;
static const char *(*f2g_strerror)(int) = NULL;
static const f2g_sensors_chip_name *(*f2g_get_detected_chips)(const f2g_sensors_chip_name *, int *) = NULL;
static const f2g_sensors_feature *(*f2g_get_features)(const f2g_sensors_chip_name *, int *) = NULL;
static const f2g_sensors_subfeature *(*f2g_get_all_subfeatures)(const f2g_sensors_chip_name *, const f2g_sensors_feature *, int *) = NULL;
static int (*f2g_get_value)(const f2g_sensors_chip_name *, int, double *) = NULL;
static const char **f2g_version = NULL;

// f2g_sensors_load opens the library with the given name and resolves all needed functions.
// Returns NULL on success, otherwise an error message (owned by the caller).
static char *f2g_sensors_load(const char *name) {
	char buf[512];
	void *h = dlopen(name, RTLD_NOW | RTLD_LOCAL);
	if (h == NULL) {
		const char *e = dlerror();
		snprintf(buf, sizeof(buf), "%s", e != NULL ? e : "dlopen failed");
		return strdup(buf);
	}
	struct { const char *sym; void **dst; } syms[] = {
		{ "sensors_init", (void **)&f2g_init },
		{ "sensors_cleanup", (void **)&f2g_cleanup },
		{ "sensors_strerror", (void **)&f2g_strerror },
		{ "sensors_get_detected_chips", (void **)&f2g_get_detected_chips },
		{ "sensors_get_features", (void **)&f2g_get_features },
		{ "sensors_get_all_subfeatures", (void **)&f2g_get_all_subfeatures },
		{ "sensors_get_value", (void **)&f2g_get_value },
		{ "libsensors_version", (void **)&f2g_version },
	};
	for (size_t i = 0; i < sizeof(syms) / sizeof(syms[0]); i++) {
		void *p = dlsym(h, syms[i].sym);
		if (p == NULL) {
			snprintf(buf, sizeof(buf), "%s: symbol %s not found", name, syms[i].sym);
			dlclose(h);
			return strdup(buf);
		}
		*syms[i].dst = p;
	}
	f2g_handle = h;
	return NULL;
}

static const char *f2g_sensors_version(void) { return *f2g_version; }

// f2g_sensors_init initializes libsensors with the given configuration file, or with an empty configuration if it
// doesn't exist (used_config is then 0). Returns 0 on success or a libsensors error code (negative). If the
// configuration file (or /dev/null) can't be opened, open_errno is set to the errno of fopen() instead.
static int f2g_sensors_init(const char *config, int *used_config, int *open_errno) {
	*used_config = 1;
	*open_errno = 0;
	FILE *f = fopen(config, "r");
	if (f == NULL && errno == ENOENT) {
		*used_config = 0;
		f = fopen("/dev/null", "r");
	}
	if (f == NULL) {
		*open_errno = errno;
		return 0;
	}
	int res = f2g_init(f);
	fclose(f);
	return res;
}
static void f2g_sensors_cleanup(void) { f2g_cleanup(); }
static const char *f2g_sensors_strerror(int err) { return f2g_strerror(err); }
static const f2g_sensors_chip_name *f2g_sensors_get_detected_chips(int *nr) { return f2g_get_detected_chips(NULL, nr); }
static const f2g_sensors_feature *f2g_sensors_get_features(const f2g_sensors_chip_name *c, int *nr) {
	return f2g_get_features(c, nr);
}
static const f2g_sensors_subfeature *f2g_sensors_get_all_subfeatures(const f2g_sensors_chip_name *c,
		const f2g_sensors_feature *f, int *nr) {
	return f2g_get_all_subfeatures(c, f, nr);
}
static int f2g_sensors_get_value(const f2g_sensors_chip_name *c, int nr, double *v) { return f2g_get_value(c, nr, v); }
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

const sensorsModeR = 1 // SENSORS_MODE_R

var (
	// mu serializes all calls into libsensors, which keeps global state
	mu         sync.Mutex
	loadOnce   sync.Once
	loadedName string
	loadErr    error
	// configUsed is true if the last DetectedChips call could read ConfigFile
	configUsed bool
)

// ConfigUsed reports whether the last DetectedChips call could read ConfigFile
func ConfigUsed() bool {
	mu.Lock()
	defer mu.Unlock()
	return configUsed
}

// openLibrary is the openFunc that loads libsensors with dlopen
func openLibrary(name string) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	if msg := C.f2g_sensors_load(cName); msg != nil {
		defer C.free(unsafe.Pointer(msg))
		return errors.New(C.GoString(msg))
	}
	return nil
}

// Load loads libsensors (once, see LibraryNames) and returns the name it was loaded by.
// The error wraps ErrUnavailable.
func Load() (string, error) {
	loadOnce.Do(func() {
		mu.Lock()
		defer mu.Unlock()
		loadedName, loadErr = loadFirst(LibraryNames, openLibrary)
	})
	return loadedName, loadErr
}

// Version returns the version of the loaded libsensors (e.g. "3.6.0"), or "" if it isn't loaded
func Version() string {
	if _, err := Load(); err != nil {
		return ""
	}
	mu.Lock()
	defer mu.Unlock()
	return C.GoString(C.f2g_sensors_version())
}

// DetectedChips returns the chips detected by libsensors, in libsensors order, with the features libsensors reports
// (features ignored in ConfigFile are left out). Values of the subfeatures selected by want are read through
// libsensors, so compute statements of ConfigFile apply to them. If ConfigFile can't be opened, libsensors runs
// without a configuration (see ConfigUsed).
// The error wraps ErrUnavailable if libsensors can't be loaded, and ErrInit if it can't be initialized (e.g. if
// ConfigFile exists but can't be read or parsed).
func DetectedChips(want WantValue) ([]Chip, error) {
	if _, err := Load(); err != nil {
		return nil, err
	}

	mu.Lock()
	defer mu.Unlock()

	cConfig := C.CString(ConfigFile)
	defer C.free(unsafe.Pointer(cConfig))
	var usedConfig, openErrno C.int
	res := C.f2g_sensors_init(cConfig, &usedConfig, &openErrno)
	if openErrno != 0 {
		return nil, fmt.Errorf("%w: opening %s failed: %w", ErrInit, ConfigFile, syscall.Errno(openErrno))
	}
	if res != 0 {
		return nil, fmt.Errorf("%w with %s: %s", ErrInit, ConfigFile, C.GoString(C.f2g_sensors_strerror(res)))
	}
	defer C.f2g_sensors_cleanup()
	configUsed = usedConfig != 0

	var chips []Chip
	var chipNr C.int
	for {
		c := C.f2g_sensors_get_detected_chips(&chipNr)
		if c == nil {
			break
		}
		chip := Chip{
			Prefix: C.GoString(c.prefix),
			Bus:    Bus{Type: int16(c.bus._type), Nr: int16(c.bus.nr)},
			Addr:   int32(c.addr),
			Path:   C.GoString(c.path),
		}

		var featureNr C.int
		for {
			f := C.f2g_sensors_get_features(c, &featureNr)
			if f == nil {
				break
			}
			feature := Feature{
				Name:   C.GoString(f.name),
				Number: int32(f.number),
				Type:   FeatureType(f._type),
			}

			var subFeatureNr C.int
			for {
				s := C.f2g_sensors_get_all_subfeatures(c, f, &subFeatureNr)
				if s == nil {
					break
				}
				subFeature := SubFeature{
					Name:   C.GoString(s.name),
					Number: int32(s.number),
					Type:   SubFeatureType(s._type),
					Flags:  uint32(s.flags),
				}
				if want != nil && want(subFeature.Type) && subFeature.Flags&sensorsModeR != 0 {
					var value C.double
					if C.f2g_sensors_get_value(c, s.number, &value) == 0 {
						subFeature.Value = float64(value)
					}
				}
				feature.SubFeatures = append(feature.SubFeatures, subFeature)
			}
			chip.Features = append(chip.Features, feature)
		}
		chips = append(chips, chip)
	}
	return chips, nil
}
