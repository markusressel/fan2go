package fans

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flatCurve returns curve data of a fan that stands still at PWM 0 and runs at rpm + noise(pwm) above
func flatCurve(rpm float64, noise func(pwm int) float64) map[int]float64 {
	result := map[int]float64{0: 0}
	for pwm := 1; pwm <= MaxPwmValue; pwm++ {
		result[pwm] = rpm + noise(pwm)
	}
	return result
}

func noNoise(int) float64 { return 0 }

// alternatingNoise returns +amplitude for even and -amplitude for odd PWM values
func alternatingNoise(amplitude float64) func(pwm int) float64 {
	return func(pwm int) float64 {
		if pwm%2 == 0 {
			return amplitude
		}
		return -amplitude
	}
}

func TestIsFlatRpmCurveData(t *testing.T) {
	// a slow fan whose RPM rises from 300 to 400 (25%), with +-10 RPM noise
	slowFan := map[int]float64{0: 0}
	for pwm := 1; pwm <= MaxPwmValue; pwm++ {
		slowFan[pwm] = 300 + 100*float64(pwm)/MaxPwmValue + alternatingNoise(10)(pwm)
	}
	// a fan that stands still below PWM 32 and rises from 221 to 839 RPM
	normalFan := map[int]float64{}
	for pwm := MinPwmValue; pwm <= MaxPwmValue; pwm++ {
		if pwm < 32 {
			normalFan[pwm] = 0
		} else {
			normalFan[pwm] = 221 + 618*float64(pwm-32)/float64(MaxPwmValue-32)
		}
	}

	tests := map[string]struct {
		curve      map[int]float64
		wantFlat   bool
		wantAvgRpm float64
	}{
		"flat, PWM 0 is ignored": {curve: flatCurve(753, noNoise), wantFlat: true, wantAvgRpm: 753},
		"flat with noise within 5% of the peak": {
			curve: flatCurve(753, alternatingNoise(15)), wantFlat: true, wantAvgRpm: 753,
		},
		// 5% of the peak is ~11 RPM, but the noise of a settled fan (2 * 20) is more
		"tiny peak with noise within 2 * maxRpmDiffForSettledFan": {
			curve: flatCurve(200, alternatingNoise(15)), wantFlat: true, wantAvgRpm: 200,
		},
		"normal fan":                 {curve: normalFan},
		"slow fan with a real slope": {curve: slowFan},
		"nil":                        {curve: nil},
		"empty":                      {curve: map[int]float64{}},
		"only PWM 0":                 {curve: map[int]float64{0: 1000}},
		"fan that never spins":       {curve: flatCurve(0, noNoise)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			flat, avgRpm := IsFlatRpmCurveData(tt.curve, 20)
			assert.Equal(t, tt.wantFlat, flat)
			if tt.wantFlat {
				assert.InDelta(t, tt.wantAvgRpm, avgRpm, 0.1)
			}
		})
	}
}

// newHwMonFanWithRpmSensor returns a hwmon fan whose RPM sensor file exists
func newHwMonFanWithRpmSensor(t *testing.T, maxPwm *int) *HwMonFan {
	t.Helper()
	rpmInput := filepath.Join(t.TempDir(), "fan1_input")
	require.NoError(t, os.WriteFile(rpmInput, []byte("753\n"), 0o644))
	return &HwMonFan{
		Config: configuration.FanConfig{
			ID: "fan", HwMon: &configuration.HwMonFanConfig{RpmInputPath: rpmInput}, MaxPwm: maxPwm,
		},
		MaxPwm: maxPwm,
	}
}

func TestHwMonFan_AttachFanRpmCurveData_FlatCurve_MaxPwmIsFullRange(t *testing.T) {
	// GIVEN: a fan that doesn't react to PWM
	fan := newHwMonFanWithRpmSensor(t, nil)
	curve := flatCurve(753, noNoise)

	// WHEN
	require.NoError(t, fan.AttachFanRpmCurveData(&curve))

	// THEN: not the lowest PWM with 98% of the peak RPM (1), which would leave a single PWM value
	assert.Equal(t, 1, fan.GetMinPwm())
	assert.Equal(t, 1, fan.GetStartPwm())
	assert.Equal(t, MaxPwmValue, fan.GetMaxPwm())
}

func TestHwMonFan_AttachFanRpmCurveData_FlatCurve_ConfiguredMaxPwmWins(t *testing.T) {
	// GIVEN
	configured := 200
	fan := newHwMonFanWithRpmSensor(t, &configured)
	curve := flatCurve(753, noNoise)

	// WHEN
	require.NoError(t, fan.AttachFanRpmCurveData(&curve))

	// THEN
	assert.Equal(t, configured, fan.GetMaxPwm())
}

func TestHwMonFan_AttachFanRpmCurveData_FlatCurve_NoRpmSensor_NotChecked(t *testing.T) {
	// GIVEN: a fan without RPM sensor
	fan := &HwMonFan{Config: configuration.FanConfig{ID: "fan", HwMon: &configuration.HwMonFanConfig{}}}
	curve := flatCurve(753, noNoise)

	// WHEN
	require.NoError(t, fan.AttachFanRpmCurveData(&curve))

	// THEN: the usual derivation
	assert.Equal(t, 1, fan.GetMaxPwm())
}

func TestWarnIfFlatRpmCurve(t *testing.T) {
	tests := map[string]struct {
		curve    *map[int]float64
		wantFlat bool
	}{
		"flat":    {curve: &map[int]float64{0: 0, 1: 753, 255: 754}, wantFlat: true},
		"normal":  {curve: &map[int]float64{0: 0, 31: 0, 32: 221, 255: 839}},
		"no data": {curve: nil},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// GIVEN
			warnings := captureWarnings(t)
			fan := newHwMonFanWithRpmSensor(t, nil)
			require.NoError(t, fan.AttachFanRpmCurveData(tt.curve))

			// WHEN
			flat := WarnIfFlatRpmCurve(fan)

			// THEN
			assert.Equal(t, tt.wantFlat, flat)
			if tt.wantFlat {
				assert.Equal(t, 1, strings.Count(warnings.String(), "WARNING"), warnings.String())
				assert.Contains(t, warnings.String(),
					"Fan fan: RPM stays at ~754 across the whole PWM range, so the fan doesn't react to PWM.")
			} else {
				assert.Empty(t, warnings.String())
			}
		})
	}
}
