//go:build cgo

package lmsensors

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSysfsChipsMatchesLibsensors compares both detections on the machine running the test. sensors3.conf can
// ignore features (libsensors leaves them out), so it only checks that sysfs finds every chip and every feature that
// libsensors finds, with the same subfeatures. Chip names differ between libsensors 3.6.0 and 3.6.2 for some
// platform devices; SysfsChips follows 3.6.2.
func TestSysfsChipsMatchesLibsensors(t *testing.T) {
	if _, err := Load(); err != nil {
		t.Skipf("libsensors not available: %v", err)
	}
	want := func(t SubFeatureType) bool { return t == SubFeatureTypeTempInput || t == SubFeatureTypeFanInput }
	fromLibsensors, err := DetectedChips(want)
	if err != nil {
		t.Skipf("libsensors can't be used: %v", err)
	}
	fromSysfs, err := SysfsChips("/sys", want)
	if err != nil {
		t.Skipf("no hwmon class in sysfs: %v", err)
	}
	if len(fromLibsensors) == 0 {
		t.Skip("no hwmon chips on this machine")
	}
	// libsensors 3.6.0 doesn't know pwm features and parses some platform device names differently
	exact := !strings.HasPrefix(Version(), "3.6.0") && !strings.HasPrefix(Version(), "3.5")
	t.Logf("libsensors %s, exact comparison: %v", Version(), exact)

	key := func(c Chip) string {
		return fmt.Sprintf("%s/%d/%d/%x/%s", c.Prefix, c.Bus.Type, c.Bus.Nr, c.Addr, c.Path)
	}
	sysfsByKey := map[string]Chip{}
	var sysfsKeys []string
	for _, c := range fromSysfs {
		sysfsByKey[key(c)] = c
		sysfsKeys = append(sysfsKeys, key(c))
	}
	var libsensorsKeys []string
	for _, c := range fromLibsensors {
		libsensorsKeys = append(libsensorsKeys, key(c))
	}
	assert.Equal(t, libsensorsKeys, sysfsKeys, "same chips in the same order")

	for _, lc := range fromLibsensors {
		sc, ok := sysfsByKey[key(lc)]
		if !ok {
			continue
		}
		sysfsFeatures := map[string]Feature{}
		for _, f := range sc.Features {
			sysfsFeatures[f.Name] = f
		}
		for _, lf := range lc.Features {
			if !exact && lf.Type != FeatureTypeTemp && lf.Type != FeatureTypeFan {
				continue
			}
			sf, ok := sysfsFeatures[lf.Name]
			if !assert.True(t, ok, "%s: feature %s", key(lc), lf.Name) {
				continue
			}
			assert.Equal(t, lf.Type, sf.Type)
			var lNames, sNames []string
			for _, s := range lf.SubFeatures {
				lNames = append(lNames, fmt.Sprintf("%s/%x/%d", s.Name, s.Type, s.Flags))
			}
			for _, s := range sf.SubFeatures {
				sNames = append(sNames, fmt.Sprintf("%s/%x/%d", s.Name, s.Type, s.Flags))
			}
			assert.Equal(t, lNames, sNames, "%s: subfeatures of %s", key(lc), lf.Name)
		}
	}
}
