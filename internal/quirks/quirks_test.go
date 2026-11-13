package quirks

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func entryById(t *testing.T, id string) *Entry {
	t.Helper()
	for _, e := range Entries() {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no registry entry %s", id)
	return nil
}

func TestRegistry_Invariants(t *testing.T) {
	ids := map[string]bool{}
	matchKeys := map[string]string{}
	for _, e := range Entries() {
		assert.NotEmpty(t, e.ID)
		assert.False(t, ids[e.ID], "duplicate id %s", e.ID)
		ids[e.ID] = true
		assert.GreaterOrEqual(t, e.Version, 1, e.ID)
		assert.NotEmpty(t, e.Description, e.ID)
		require.NotEmpty(t, e.Sources, e.ID)
		assert.False(t, strings.Contains(e.Sources[0], "github.com/markusressel/fan2go/"),
			"%s: the first source must be a primary source (kernel docs or driver source)", e.ID)
		require.NotEmpty(t, e.Match.Names, e.ID)
		for _, name := range e.Match.Names {
			key := name + "|" + e.Match.Modalias
			other, exists := matchKeys[key]
			assert.False(t, exists, "%s and %s match the same devices", e.ID, other)
			matchKeys[key] = e.ID
		}
		if e.Readback != nil {
			previous := -1
			for v := 0; v <= 255; v++ {
				r := e.Readback(v)
				assert.True(t, r >= 0 && r <= 255, "%s: readback of %d out of range: %d", e.ID, v, r)
				assert.GreaterOrEqual(t, r, previous, "%s: readback not monotonic at %d", e.ID, v)
				previous = r
			}
			assert.Equal(t, 0, e.Readback(0), e.ID)
			assert.Equal(t, 255, e.Readback(255), e.ID)
		}
	}
}

func TestRegistry_Formulas(t *testing.T) {
	corsair := entryById(t, "corsaircpro")
	assert.Equal(t, 250, corsair.Readback(249))
	assert.Equal(t, 3, corsair.Readback(2))
	assert.Equal(t, 0, corsair.Readback(1))
	assert.Len(t, corsair.OutputLevels(), 101)

	aquacomputer := entryById(t, "aquacomputer")
	for v := 0; v <= 255; v++ {
		assert.Equal(t, v, aquacomputer.Readback(v))
	}

	// the readback differs between ThinkPad models, only the number of levels is known
	thinkpad := entryById(t, "thinkpad")
	assert.Nil(t, thinkpad.Readback)
	assert.Nil(t, thinkpad.OutputLevels())
	assert.Equal(t, 8, thinkpad.LevelCount())
	assert.Equal(t, 32.0, thinkpad.WriteStep())

	assert.Equal(t, 101, corsair.LevelCount())
	assert.Equal(t, 2.0, corsair.WriteStep())
	assert.Equal(t, 256, aquacomputer.LevelCount())
	assert.Equal(t, 1.0, aquacomputer.WriteStep())

	nzxt := entryById(t, "nzxtsmart2")
	assert.Equal(t, 3, nzxt.Readback(1)) // positive values never become 0: 1 -> 1% -> 3
	assert.Equal(t, 250, nzxt.Readback(249))
	assert.Len(t, nzxt.OutputLevels(), 101)
}

func TestRegistry_It87FullDutyReadsAsFullSpeed(t *testing.T) {
	tests := []struct {
		name       string
		pwmChannel int
		expected   bool
	}{
		// no FEAT_FANCTL_ONOFF: all channels
		{"it8665", 1, true},
		{"it8665", 6, true},
		{"it8603", 1, true},
		{"it8613", 2, true},
		{"it8622", 3, true},
		{"it8606", 1, true},
		{"it8625", 1, true},
		{"it8655", 1, true},
		{"it8686", 1, true},
		{"it8688", 1, true},
		{"it8689", 1, true},
		{"it8696", 1, true},
		{"it8698", 1, true},
		// FEAT_FANCTL_ONOFF with more than 3 pwm channels: pwm4 and above
		{"it8620", 3, false},
		{"it8620", 4, true},
		{"it8628", 3, false},
		{"it8628", 6, true},
		{"it8732", 3, false},
		{"it8732", 4, true},
		// unknown channel
		{"it8665", 0, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/pwm%d", tt.name, tt.pwmChannel), func(t *testing.T) {
			e := Lookup(tt.name, "platform:it87")
			require.NotNil(t, e)
			assert.Equal(t, "it87", e.ID)
			assert.Equal(t, tt.expected, e.FullDutyReadsAsFullSpeed(tt.name, tt.pwmChannel))
		})
	}

	// FEAT_FANCTL_ONOFF and at most 3 pwm channels: no entry
	for _, name := range []string{"it87", "it8712", "it8716", "it8718", "it8720", "it8721", "it8728", "it8736",
		"it8738", "it8771", "it8772", "it8781", "it8782", "it8783", "it8785", "it8786", "it8790", "it8792", "it8607",
		"it87952"} {
		assert.Nil(t, Lookup(name, "platform:it87"), name)
	}
	// only the it87 driver: on other drivers, 0 can mean full speed without pwm control
	assert.Nil(t, Lookup("it8665", "platform:nct6775"))
	assert.Nil(t, Lookup("it8665", ""))
	assert.Nil(t, Lookup("nct6798", "platform:it87"))
	assert.NotNil(t, Lookup("it8665", "platform:it87.656"))
	// other entries don't have this behaviour
	assert.False(t, entryById(t, "nzxtsmart2").FullDutyReadsAsFullSpeed("nzxtsmart2", 1))
	var none *Entry
	assert.False(t, none.FullDutyReadsAsFullSpeed("it8665", 1))
}

func TestAppliedMessage_FullDutyReadsAsFullSpeed(t *testing.T) {
	e := entryById(t, "it87")
	config := configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{
		HwmonName: "it8628", Modalias: "platform:it87", PwmChannel: 4,
	}}
	assert.Contains(t, e.AppliedMessage(config), "pwm_enable 0 at pwm 255 counts as manual mode")
	assert.Contains(t, e.AppliedMessage(config), "a hint if fan2go can't take control of the fan")

	config.HwMon.PwmChannel = 1
	assert.NotContains(t, e.AppliedMessage(config), "pwm_enable 0 at pwm 255")
}

func TestLookup(t *testing.T) {
	entries := []*Entry{
		{ID: "generic", Match: Match{Names: []string{"chip"}}},
		{ID: "specific", Match: Match{Names: []string{"chip"}, Modalias: "platform:chip_oot*"}},
		{ID: "other", Match: Match{Names: []string{"other", "other2"}}},
	}
	tests := []struct {
		name, modalias, expected string
	}{
		{"chip", "platform:chip", "generic"},
		{"chip", "platform:chip_oot", "specific"},
		{"chip", "platform:chip_oot.1", "specific"},
		{"chip", "", "generic"},
		{"other2", "", "other"},
		{"Chip", "platform:chip", ""}, // exact, case-sensitive
		{"chip2", "platform:chip", ""},
		{"", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.modalias, func(t *testing.T) {
			e := lookup(entries, tt.name, tt.modalias)
			if tt.expected == "" {
				assert.Nil(t, e)
			} else {
				require.NotNil(t, e)
				assert.Equal(t, tt.expected, e.ID)
			}
		})
	}
}

func hwmonFanConfig(name string) configuration.FanConfig {
	return configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{HwmonName: name}}
}

func TestForFan(t *testing.T) {
	assert.Equal(t, "thinkpad", ForFan(hwmonFanConfig("thinkpad")).ID)
	assert.Nil(t, ForFan(hwmonFanConfig("nct6798")))
	assert.Nil(t, ForFan(configuration.FanConfig{ID: "fan", File: &configuration.FileFanConfig{}}))

	disabled := hwmonFanConfig("thinkpad")
	disabled.Quirks = &configuration.QuirksConfig{}
	disabled.Quirks.Enabled.SetOverride(false)
	assert.Nil(t, ForFan(disabled))

	enabled := hwmonFanConfig("thinkpad")
	enabled.Quirks = &configuration.QuirksConfig{}
	assert.NotNil(t, ForFan(enabled))
}

func TestFingerprint(t *testing.T) {
	var none *Entry
	id, version := none.Fingerprint()
	assert.Equal(t, "", id)
	assert.Equal(t, 0, version)
	id, version = (&Entry{ID: "x", Version: 3}).Fingerprint()
	assert.Equal(t, "x", id)
	assert.Equal(t, 3, version)
}

func TestAppliedMessage(t *testing.T) {
	e := entryById(t, "corsaircpro")
	config := hwmonFanConfig("corsaircpro")

	msg := e.AppliedMessage(config)
	assert.Contains(t, msg, "'corsaircpro' v1")
	assert.Contains(t, msg, "no set/get sweep")
	assert.Contains(t, msg, "https://github.com/markusressel/fan2go/issues/64")
	assert.Contains(t, msg, "quirks.enabled: false")
	assert.NotContains(t, msg, "overridden")

	config.SetPwmToGetPwmMap = &configuration.SetPwmToGetPwmMapConfig{Autodetect: &configuration.SetPwmToGetPwmMapAutodetectConfig{}}
	assert.NotContains(t, e.AppliedMessage(config), "overridden")

	config.SetPwmToGetPwmMap = &configuration.SetPwmToGetPwmMapConfig{Identity: &configuration.SetPwmToGetPwmMapIdentityConfig{}}
	assert.Contains(t, e.AppliedMessage(config), "overridden by config: setPwmToGetPwmMap")

	adviceOnly := &Entry{ID: "x", Version: 1, Description: "X", Sources: []string{"https://docs.kernel.org/x"}}
	assert.Contains(t, adviceOnly.AppliedMessage(config), "only advice in 'fan2go config validate'")
}

func TestUsesReadback(t *testing.T) {
	corsair := entryById(t, "corsaircpro")
	config := hwmonFanConfig("corsaircpro")
	assert.True(t, corsair.UsesReadback(config, false))
	assert.False(t, corsair.UsesReadback(config, true))
	assert.False(t, entryById(t, "thinkpad").UsesReadback(config, false))
	var none *Entry
	assert.False(t, none.UsesReadback(config, false))

	config.SetPwmToGetPwmMap = &configuration.SetPwmToGetPwmMapConfig{Autodetect: &configuration.SetPwmToGetPwmMapAutodetectConfig{}}
	assert.True(t, corsair.UsesReadback(config, false), "autodetect is the same as no setPwmToGetPwmMap")

	config.SetPwmToGetPwmMap = &configuration.SetPwmToGetPwmMapConfig{Linear: &configuration.SetPwmToGetPwmMapLinearConfig{0: 0, 255: 255}}
	assert.False(t, corsair.UsesReadback(config, false))
}

func messages(advice []Advice) []string {
	var result []string
	for _, a := range advice {
		result = append(result, a.Message)
	}
	return result
}

func noFile(string) ([]byte, error) { return nil, errors.New("no such file") }

func TestAdvice_ReadbackFormula(t *testing.T) {
	e := entryById(t, "aquacomputer")
	config := hwmonFanConfig("d5next")
	assert.Empty(t, e.Check(AdviceContext{Fan: config}))

	// autodetect is the same as no setPwmToGetPwmMap: the formula is used
	config.SetPwmToGetPwmMap = &configuration.SetPwmToGetPwmMapConfig{Autodetect: &configuration.SetPwmToGetPwmMapAutodetectConfig{}}
	assert.Empty(t, e.Check(AdviceContext{Fan: config}))

	config.SetPwmToGetPwmMap = &configuration.SetPwmToGetPwmMapConfig{Values: &configuration.SetPwmToGetPwmMapValuesConfig{0: 0, 255: 255}}
	advice := e.Check(AdviceContext{Fan: config})
	require.Len(t, advice, 1)
	assert.Equal(t, AdviceTip, advice[0].Level)
	assert.Contains(t, advice[0].Message, "Remove setPwmToGetPwmMap")
}

func directConfig(direct configuration.DirectControlAlgorithmConfig) *configuration.ControlAlgorithmConfig {
	return &configuration.ControlAlgorithmConfig{Direct: &direct}
}

func TestAdvice_SmoothingResolution(t *testing.T) {
	thinkpad := entryById(t, "thinkpad")
	corsair := entryById(t, "corsaircpro")
	tick := 200 * time.Millisecond
	fanControlOn := func(string) ([]byte, error) { return []byte("Y\n"), nil }

	tests := []struct {
		name      string
		entry     *Entry
		algorithm *configuration.ControlAlgorithmConfig
		expected  []string
	}{
		{"thinkpad: no controlAlgorithm", thinkpad, nil, nil},
		{"thinkpad: pid", thinkpad, &configuration.ControlAlgorithmConfig{Pid: &configuration.PidControlAlgorithmConfig{}}, nil},
		{"thinkpad: deadband 1%", thinkpad, directConfig(configuration.DirectControlAlgorithmConfig{Deadband: 2.55}), []string{"deadband is 2.55", "8 output levels"}},
		{"thinkpad: deadband just below one step", thinkpad, directConfig(configuration.DirectControlAlgorithmConfig{Deadband: 31.9}), []string{"at least 32 of 255", "up to one level away from the curve"}},
		{"thinkpad: deadband one step", thinkpad, directConfig(configuration.DirectControlAlgorithmConfig{Deadband: 32}), nil},
		{"thinkpad: slow rampDown", thinkpad, directConfig(configuration.DirectControlAlgorithmConfig{RampDown: 5.1}), []string{"rampDown is 5.1 per second", "one every 6.3s"}},
		{"thinkpad: fast rampUp", thinkpad, directConfig(configuration.DirectControlAlgorithmConfig{RampUp: 255}), nil},
		{"corsair: deadband 1%", corsair, directConfig(configuration.DirectControlAlgorithmConfig{Deadband: 2.55}), nil},
		{"corsair: deadband 0.5%", corsair, directConfig(configuration.DirectControlAlgorithmConfig{Deadband: 1.275}), []string{"101 output levels", "use a deadband of at least 2"}},
		{"corsair: deadband 2", corsair, directConfig(configuration.DirectControlAlgorithmConfig{Deadband: 2}), nil},
		// less than one step per cycle, but less than 2 s per step: not noticeable
		{"corsair: rampUp 1%/s", corsair, directConfig(configuration.DirectControlAlgorithmConfig{RampUp: 2.55}), nil},
		{"corsair: rampUp 0.5 per second", corsair, directConfig(configuration.DirectControlAlgorithmConfig{RampUp: 0.5}), []string{"rampUp is 0.5 per second", "one every 4s"}},
		{"thinkpad: rampUp 20 per second", thinkpad, directConfig(configuration.DirectControlAlgorithmConfig{RampUp: 20}), nil},
		{"corsair: rampUp 10%/s", corsair, directConfig(configuration.DirectControlAlgorithmConfig{RampUp: 25.5}), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := hwmonFanConfig(tt.entry.Match.Names[0])
			config.ControlAlgorithm = tt.algorithm

			advice := tt.entry.Check(AdviceContext{Fan: config, AdjustmentTickRate: tick, ReadFile: fanControlOn})

			if tt.expected == nil {
				assert.Empty(t, messages(advice))
				return
			}
			require.Len(t, advice, 1, "%v", messages(advice))
			assert.Equal(t, AdviceTip, advice[0].Level)
			for _, s := range tt.expected {
				assert.Contains(t, advice[0].Message, s)
			}
		})
	}
}

func TestAdvice_OnlyPwmControlMode(t *testing.T) {
	e := entryById(t, "nzxtsmart2")
	mode := func(v string) *configuration.ControlModeValue {
		value := configuration.ControlModeValue(v)
		return &value
	}
	tests := []struct {
		name     string
		cfg      *configuration.ControlModeConfig
		expected []string
	}{
		{"none", nil, nil},
		{"active pwm", &configuration.ControlModeConfig{Active: mode("pwm")}, nil},
		{"active 1", &configuration.ControlModeConfig{Active: mode("1")}, nil},
		{"active 01", &configuration.ControlModeConfig{Active: mode("01")}, nil},
		{"active 2", &configuration.ControlModeConfig{Active: mode("2")}, []string{"controlMode.active is '2'"}},
		{"active disabled", &configuration.ControlModeConfig{Active: mode("disabled")}, []string{"controlMode.active is 'disabled'"}},
		{"onExit auto", &configuration.ControlModeConfig{OnExit: &configuration.OnExitConfig{ControlMode: mode("auto")}}, []string{"controlMode.onExit.mode is 'auto'"}},
		{"onExit restore", &configuration.ControlModeConfig{OnExit: &configuration.OnExitConfig{Restore: &configuration.OnExitRestoreConfig{}}}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := hwmonFanConfig("nzxtsmart2")
			config.ControlMode = tt.cfg

			advice := e.Check(AdviceContext{Fan: config})

			assert.Len(t, advice, len(tt.expected), "%v", messages(advice))
			for i, s := range tt.expected {
				assert.Equal(t, AdviceWarning, advice[i].Level)
				assert.Contains(t, advice[i].Message, s)
			}
		})
	}
}

func TestAdvice_ThinkpadFanControl(t *testing.T) {
	e := entryById(t, "thinkpad")
	config := hwmonFanConfig("thinkpad")
	param := func(content string) func(string) ([]byte, error) {
		return func(name string) ([]byte, error) {
			assert.Equal(t, "/sys/module/thinkpad_acpi/parameters/fan_control", name)
			return []byte(content), nil
		}
	}

	assert.Empty(t, e.Check(AdviceContext{Fan: config, ReadFile: param("Y\n")}))

	advice := e.Check(AdviceContext{Fan: config, ReadFile: param("N\n")})
	require.Len(t, advice, 1)
	assert.Equal(t, AdviceWarning, advice[0].Level)
	assert.Contains(t, advice[0].Message, "options thinkpad_acpi fan_control=1")

	advice = e.Check(AdviceContext{Fan: config, ReadFile: noFile})
	require.Len(t, advice, 1)
	assert.Equal(t, AdviceTip, advice[0].Level)
	assert.Contains(t, advice[0].Message, "fan_control=1")
}
