// Package quirks is fan2go's registry of known hardware quirks: driver behaviour that fan2go can't detect
// reliably, but that is documented in the kernel docs or the driver source of a specific device.
//
// Entries are identified exactly, by the hwmon name of the device (and optionally its modalias), never by
// heuristics. See docs/hardware-quirks.md for the rules for adding an entry.
package quirks

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/markusressel/fan2go/internal/configuration"
)

// Match identifies the hwmon devices an Entry applies to.
type Match struct {
	// Names are the exact contents of /sys/class/hwmon/hwmonN/name the entry applies to.
	Names []string
	// Modalias optionally restricts the entry to devices whose device/modalias matches this glob
	// (path.Match syntax, e.g. "platform:nct6687"). Empty = any modalias.
	Modalias string
}

// specificity is the number of match fields an entry sets. The most specific matching entry wins.
func (m Match) specificity() int {
	result := 1
	if m.Modalias != "" {
		result++
	}
	return result
}

func (m Match) matches(name string, modalias string) bool {
	found := false
	for _, n := range m.Names {
		if n == name {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	if m.Modalias == "" {
		return true
	}
	ok, err := path.Match(m.Modalias, modalias)
	return err == nil && ok
}

// Entry describes the known quirks of one device family.
type Entry struct {
	// ID identifies the entry in logs, `fan2go detect`, the REST API and the database.
	ID string
	// Version is incremented on every change of Readback. A changed ID or Version makes fan2go analyze the fans
	// of the device again, if they use Readback (see UsesReadback).
	Version int
	// Description names the device(s) and the driver, e.g. "Corsair Commander Pro (corsair-cpro)".
	Description string
	// Sources are links to the primary sources (kernel docs, driver source) first, then to fan2go issues.
	Sources []string
	// Match identifies the devices.
	Match Match

	// Readback is the formula the driver uses to report a written pwm value when it is read back,
	// nil if it is not known. If set, fan2go uses it as the setPwmToGetPwmMap of the fan, instead of
	// writing all values from 0 to 255 to measure it.
	Readback func(set int) int
	// Levels is the number of distinct output levels of the device, if it is known but Readback isn't
	// (0 = unknown, or derived from Readback). Only used for advice.
	Levels int
	// Hint is added to the error when fan2go can't take control of the fan.
	Hint string
	// FullDutyReadsAsFullSpeedFromChannel maps hwmon names of the entry to the first pwm channel (1-based) whose
	// pwm_enable reads 0 ("full speed") instead of 1 while the channel is in manual mode at pwm 255. Lower channels,
	// and names that are not in the map, don't do this. nil if the driver never does this.
	// See FullDutyReadsAsFullSpeed.
	FullDutyReadsAsFullSpeedFromChannel map[string]int
	// Advice checks a fan config for settings that don't work well with this device, see Check.
	Advice []AdviceFunc
}

// Fingerprint identifies the behaviour of an entry, for the analysis data that was measured with it.
// It is ("", 0) for no entry.
func (e *Entry) Fingerprint() (id string, version int) {
	if e == nil {
		return "", 0
	}
	return e.ID, e.Version
}

// SetPwmToGetPwmMap returns the set->get PWM map from Readback for all values 0..255, nil if Readback is nil.
func (e *Entry) SetPwmToGetPwmMap() map[int]int {
	if e == nil || e.Readback == nil {
		return nil
	}
	result := make(map[int]int, 256)
	for v := 0; v <= 255; v++ {
		result[v] = e.Readback(v)
	}
	return result
}

// OutputLevels returns the distinct values the device reports for the writes 0..255 (sorted),
// i.e. its real output levels, nil if Readback is nil.
func (e *Entry) OutputLevels() []int {
	m := e.SetPwmToGetPwmMap()
	if m == nil {
		return nil
	}
	seen := map[int]bool{}
	var result []int
	for _, v := range m {
		if !seen[v] {
			seen[v] = true
			result = append(result, v)
		}
	}
	sort.Ints(result)
	return result
}

// LevelCount returns the number of distinct output levels of the device, 0 if unknown.
func (e *Entry) LevelCount() int {
	if levels := e.OutputLevels(); levels != nil {
		return len(levels)
	}
	return e.Levels
}

// WriteStep returns the smallest change of the written pwm value that can change the output of the device,
// 0 if unknown: the shortest run of written values with the same readback (Readback), or 256 / Levels.
func (e *Entry) WriteStep() float64 {
	if e.Readback != nil {
		shortest := 256
		run := 1
		for v := 1; v <= 255; v++ {
			if e.Readback(v) == e.Readback(v-1) {
				run++
				continue
			}
			// the first and the last run may be cut off by the range, only inner runs count
			if v-run > 0 {
				shortest = min(shortest, run)
			}
			run = 1
		}
		return float64(shortest)
	}
	if e.Levels > 0 {
		return 256.0 / float64(e.Levels)
	}
	return 0
}

// UsesReadback returns whether fan2go uses the Readback formula of the entry for the given fan config
// (it has one, and neither a configured setPwmToGetPwmMap nor assumePwmMapIdentity overrides it).
// setPwmToGetPwmMap: autodetect counts as not configured.
func (e *Entry) UsesReadback(config configuration.FanConfig, assumePwmMapIdentity bool) bool {
	return e != nil && e.Readback != nil && !hasSetPwmToGetPwmMap(config) && !assumePwmMapIdentity
}

// hasSetPwmToGetPwmMap returns whether the fan config sets a setPwmToGetPwmMap (identity, linear or values).
// autodetect is the same as no setPwmToGetPwmMap.
func hasSetPwmToGetPwmMap(config configuration.FanConfig) bool {
	return config.SetPwmToGetPwmMap != nil && config.SetPwmToGetPwmMap.Autodetect == nil
}

// FullDutyReadsAsFullSpeed returns whether pwm_enable of the given pwm channel (1-based) of the device with the
// given hwmon name reads 0 instead of 1 while the channel is in manual mode at pwm 255 (see
// FullDutyReadsAsFullSpeedFromChannel). fan2go then treats pwm_enable 0 together with pwm 255 as manual mode.
func (e *Entry) FullDutyReadsAsFullSpeed(hwmonName string, pwmChannel int) bool {
	if e == nil || pwmChannel <= 0 {
		return false
	}
	first, ok := e.FullDutyReadsAsFullSpeedFromChannel[hwmonName]
	return ok && pwmChannel >= first
}

// IssueLink returns the first fan2go issue in Sources, or the first source if there is none.
func (e *Entry) IssueLink() string {
	for _, s := range e.Sources {
		if strings.Contains(s, "github.com/markusressel/fan2go/") {
			return s
		}
	}
	if len(e.Sources) > 0 {
		return e.Sources[0]
	}
	return ""
}

// AppliedMessage returns the log message for a fan the entry is applied to: what fan2go does differently,
// which of that the fan config overrides, and where to find more information.
func (e *Entry) AppliedMessage(config configuration.FanConfig) string {
	var effects []string
	if e.Readback != nil {
		effect := "set->get PWM map from the driver formula, no set/get sweep"
		if hasSetPwmToGetPwmMap(config) {
			effect += " (overridden by config: setPwmToGetPwmMap)"
		}
		effects = append(effects, effect)
	}
	if config.HwMon != nil && e.FullDutyReadsAsFullSpeed(config.HwMon.HwmonName, config.HwMon.PwmChannel) {
		effects = append(effects, "pwm_enable 0 at pwm 255 counts as manual mode")
	}
	if e.Hint != "" {
		effects = append(effects, "a hint if fan2go can't take control of the fan")
	}
	if len(effects) == 0 {
		effects = append(effects, "only advice in 'fan2go config validate'")
	}
	return fmt.Sprintf("Fan %s: applying known hardware quirks '%s' v%d (%s): %s. See %s. "+
		"Disable with 'quirks.enabled: false'.",
		config.ID, e.ID, e.Version, e.Description, strings.Join(effects, "; "), e.IssueLink())
}

// Lookup returns the most specific registry entry for the given hwmon name and modalias, nil if none matches.
func Lookup(name string, modalias string) *Entry {
	return lookup(registry, name, modalias)
}

// lookup returns the most specific matching entry. Of several equally specific entries, the first one wins; the
// registry must not contain such overlaps (TestRegistry_Invariants rejects equal match keys).
func lookup(entries []*Entry, name string, modalias string) *Entry {
	if name == "" {
		return nil
	}
	var best *Entry
	for _, e := range entries {
		if !e.Match.matches(name, modalias) {
			continue
		}
		if best == nil || e.Match.specificity() > best.Match.specificity() {
			best = e
		}
	}
	return best
}

// Entries returns all registry entries.
func Entries() []*Entry {
	return append([]*Entry(nil), registry...)
}

// ForFan returns the registry entry that applies to the given fan config, nil if none applies:
// the fan is not a resolved hwmon fan, its device has no entry, or the config disables the quirks.
func ForFan(config configuration.FanConfig) *Entry {
	if config.HwMon == nil || !config.QuirksEnabled() {
		return nil
	}
	return Lookup(config.HwMon.HwmonName, config.HwMon.Modalias)
}
