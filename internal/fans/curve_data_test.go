package fans

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pterm/pterm"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// curveStartingAt returns curve data of a fan that stands still below startPwm and spins
// with 1000 RPM from startPwm on.
func curveStartingAt(startPwm int) map[int]float64 {
	result := map[int]float64{}
	for pwm := MinPwmValue; pwm <= MaxPwmValue; pwm++ {
		if pwm >= startPwm {
			result[pwm] = 1000
		} else {
			result[pwm] = 0
		}
	}
	return result
}

func TestHwMonFan_AttachFanRpmCurveData_Twice_RecomputesStartPwm(t *testing.T) {
	// GIVEN: a fan without a configured startPwm, analyzed once
	fan := &HwMonFan{Config: configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{}}}
	first := curveStartingAt(50)
	require.NoError(t, fan.AttachFanRpmCurveData(&first))
	require.Equal(t, 50, fan.GetStartPwm())

	// WHEN: it is analyzed again and starts at a different PWM value now
	second := curveStartingAt(100)
	err := fan.AttachFanRpmCurveData(&second)

	// THEN: the start PWM is computed from the new data, not kept from the previous one
	assert.NoError(t, err)
	assert.Equal(t, 100, fan.GetStartPwm())
	startPwm, _ := ComputePwmBoundaries(fan)
	assert.Equal(t, 100, startPwm)
}

func TestHwMonFan_AttachFanRpmCurveData_Twice_KeepsConfiguredStartPwm(t *testing.T) {
	// GIVEN: a fan with a configured startPwm
	configured := 70
	fan := &HwMonFan{
		Config:   configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{}, StartPwm: &configured},
		StartPwm: &configured,
	}
	first := curveStartingAt(50)
	require.NoError(t, fan.AttachFanRpmCurveData(&first))

	// WHEN
	second := curveStartingAt(100)
	err := fan.AttachFanRpmCurveData(&second)

	// THEN: the configured value always wins
	assert.NoError(t, err)
	assert.Equal(t, configured, fan.GetStartPwm())
	startPwm, _ := ComputePwmBoundaries(fan)
	assert.Equal(t, configured, startPwm)
}

func TestComputePwmBoundaries_NoCurveData(t *testing.T) {
	// GIVEN: a fan without RPM sensor, which has no curve data
	fan := &HwMonFan{Config: configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{}}}
	require.NoError(t, fan.AttachFanRpmCurveData(nil))

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, MaxPwmValue, startPwm)
	assert.Equal(t, MaxPwmValue, maxPwm)
}

func TestComputePwmBoundaries_IgnoresComputedStartPwm(t *testing.T) {
	// GIVEN: a fan whose current start PWM was computed from older curve data
	computed := 30
	fan := &HwMonFan{
		Config:   configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{}},
		StartPwm: &computed,
	}
	data := curveStartingAt(100)
	fan.FanCurveData = &data

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN: the curve is flat from PWM 100, so that is also the lowest PWM with the peak RPM
	assert.Equal(t, 100, startPwm)
	assert.Equal(t, 100, maxPwm)
}

func TestHwMonFan_AttachFanRpmCurveData_ClampsDerivedPwmBoundaries(t *testing.T) {
	intPtr := func(v int) *int { return &v }
	tests := []struct {
		name                        string
		minPwm, startPwm, maxPwm    *int
		curveData                   map[int]float64
		wantMin, wantStart, wantMax int
		// wantWarnings are substrings of the expected warnings, none means no warning
		wantWarnings []string
	}{
		{
			// the curve is flat from PWM 50, so that is also the lowest PWM with the peak RPM
			name:      "no conflict",
			curveData: curveStartingAt(50),
			wantMin:   50, wantStart: 50, wantMax: 50,
		},
		{
			// the scaling of curve values to [minPwm..maxPwm] would be inverted
			name:      "measured start above the configured maxPwm",
			maxPwm:    intPtr(100),
			curveData: curveStartingAt(120),
			wantMin:   100, wantStart: 100, wantMax: 100,
			wantWarnings: []string{
				"Fan fan: the derived minPwm (120) is above maxPwm (100), using minPwm 100",
				"Fan fan: the derived startPwm (120) is above maxPwm (100), using startPwm 100",
			},
		},
		{
			name:      "configured minPwm and maxPwm, measured start above maxPwm",
			minPwm:    intPtr(80),
			maxPwm:    intPtr(100),
			curveData: curveStartingAt(120),
			wantMin:   80, wantStart: 100, wantMax: 100,
			wantWarnings: []string{"Fan fan: the derived startPwm (120) is above maxPwm (100), using startPwm 100"},
		},
		{
			name:      "configured minPwm above the measured maxPwm",
			minPwm:    intPtr(150),
			curveData: map[int]float64{0: 0, 50: 1000, 120: 2000},
			wantMin:   150, wantStart: 50, wantMax: 150,
			wantWarnings: []string{"Fan fan: the derived maxPwm is below minPwm (150) or startPwm (50), using maxPwm 150"},
		},
		{
			name:      "configured startPwm above the measured maxPwm",
			startPwm:  intPtr(150),
			curveData: map[int]float64{0: 0, 50: 1000, 120: 2000},
			wantMin:   150, wantStart: 150, wantMax: 150,
			wantWarnings: []string{"Fan fan: the derived maxPwm is below minPwm (150) or startPwm (150), using maxPwm 150"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			warnings := captureWarnings(t)
			fan := &HwMonFan{
				Config: configuration.FanConfig{
					ID: "fan", HwMon: &configuration.HwMonFanConfig{},
					MinPwm: tt.minPwm, StartPwm: tt.startPwm, MaxPwm: tt.maxPwm,
				},
				MinPwm: tt.minPwm, StartPwm: tt.startPwm, MaxPwm: tt.maxPwm,
			}

			// WHEN
			err := fan.AttachFanRpmCurveData(&tt.curveData)

			// THEN
			assert.NoError(t, err)
			assert.Equal(t, tt.wantMin, fan.GetMinPwm(), "minPwm")
			assert.Equal(t, tt.wantStart, fan.GetStartPwm(), "startPwm")
			assert.Equal(t, tt.wantMax, fan.GetMaxPwm(), "maxPwm")
			for _, w := range tt.wantWarnings {
				assert.Contains(t, warnings.String(), w)
			}
			assert.Equal(t, len(tt.wantWarnings), strings.Count(warnings.String(), "WARNING"), warnings.String())
		})
	}
}

// captureWarnings redirects warnings logged via ui.Warning into the returned buffer for the duration of the test
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var warnings bytes.Buffer
	oldWarning := pterm.Warning
	pterm.Warning = *pterm.Warning.WithWriter(&warnings)
	t.Cleanup(func() { pterm.Warning = oldWarning })
	return &warnings
}

func TestComputePwmBoundariesFromCurveData_SaturatingFan_LowestPwmWithTheRatio(t *testing.T) {
	// GIVEN: a fan that starts at PWM 50 (1000 RPM) and saturates at PWM 150 (2000 RPM)
	curve := map[int]float64{}
	for pwm := MinPwmValue; pwm <= MaxPwmValue; pwm++ {
		switch {
		case pwm < 50:
			curve[pwm] = 0
		case pwm < 150:
			curve[pwm] = 1000 + 10*float64(pwm-50)
		default:
			curve[pwm] = 2000
		}
	}

	// WHEN
	startPwm, maxPwm := ComputePwmBoundariesFromCurveData(curve, MaxPwmValue)

	// THEN: 98% of 2000 RPM = 1960 RPM, reached at PWM 146; the flat range above it is cut off
	assert.Equal(t, 50, startPwm)
	assert.Equal(t, 146, maxPwm)
}

func TestComputePwmBoundariesFromCurveData_NoSpinningValue_MaxPwmIs255(t *testing.T) {
	startPwm, maxPwm := ComputePwmBoundariesFromCurveData(map[int]float64{0: 0, 255: 10}, MaxPwmValue)

	assert.Equal(t, MaxPwmValue, startPwm)
	assert.Equal(t, MaxPwmValue, maxPwm)
}

func TestFailSafeMaxPwm(t *testing.T) {
	configured := 200
	derived := 150
	tests := map[string]struct {
		fan  Fan
		want int
	}{
		"hwmon, derived maxPwm": {
			fan:  &HwMonFan{Config: configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{}}, MaxPwm: &derived},
			want: MaxPwmValue,
		},
		"hwmon, configured maxPwm": {
			fan:  &HwMonFan{Config: configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{}, MaxPwm: &configured}, MaxPwm: &configured},
			want: configured,
		},
		"file fan": {
			fan:  &FileFan{Config: configuration.FanConfig{ID: "fan", File: &configuration.FileFanConfig{}}},
			want: MaxPwmValue,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, FailSafeMaxPwm(tt.fan))
		})
	}
}
