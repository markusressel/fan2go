package fan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMeasuredPwmBoundaries(t *testing.T) {
	// a hwmon fan with an RPM sensor
	rpmInput := filepath.Join(t.TempDir(), "fan1_input")
	require.NoError(t, os.WriteFile(rpmInput, []byte("753\n"), 0o644))
	fan := &fans.HwMonFan{Config: configuration.FanConfig{
		ID: "fan", HwMon: &configuration.HwMonFanConfig{RpmInputPath: rpmInput},
	}}

	flat := map[int]float64{0: 0}
	normal := map[int]float64{}
	for pwm := 1; pwm <= fans.MaxPwmValue; pwm++ {
		flat[pwm] = 753
	}
	for pwm := fans.MinPwmValue; pwm <= fans.MaxPwmValue; pwm++ {
		normal[pwm] = 0
		if pwm >= 50 {
			normal[pwm] = 1000
		}
	}

	tests := map[string]struct {
		curve                    map[int]float64
		wantStartPwm, wantMaxPwm string
	}{
		// not the lowest PWM with 98% of the peak RPM (1)
		"flat curve":   {curve: flat, wantStartPwm: "1", wantMaxPwm: "255"},
		"normal curve": {curve: normal, wantStartPwm: "50", wantMaxPwm: "50"},
		"no data":      {curve: nil, wantStartPwm: "-", wantMaxPwm: "-"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			startPwm, maxPwm := measuredPwmBoundaries(fan, tt.curve)
			assert.Equal(t, tt.wantStartPwm, startPwm)
			assert.Equal(t, tt.wantMaxPwm, maxPwm)
		})
	}
}
