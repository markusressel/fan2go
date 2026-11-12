package fans

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file exercise a fan the same way the daemon does: the RPM monitor
// of a fan controller reads the RPM and updates the moving average in one goroutine, while
// the update loop, the statistics collector and the REST API access the same fan from
// other goroutines. Run them with -race.

const concurrencyIterations = 200

// runRpmActor mimics the RPM monitor of a fan controller and the statistics collector.
func runRpmActor(fan Fan, iterations int) {
	for i := 0; i < iterations; i++ {
		rpm, _ := fan.GetRpm()
		avg := util.UpdateExponentialMovingAvg(fan.GetRpmAvg(), 10, float64(rpm))
		fan.SetRpmAvg(avg)
		_, _ = fan.GetPwm()
		if curveData := fan.GetFanRpmCurveData(); curveData != nil {
			for range *curveData {
			}
		}
	}
}

// runUpdateActor mimics the update loop of a fan controller, the statistics collector and the REST API.
func runUpdateActor(t *testing.T, fan Fan, iterations int) {
	for i := 0; i < iterations; i++ {
		_ = fan.SetPwm(100 + i%50)
		_, _ = fan.GetPwm()
		fan.SetRpmAvg(1)
		_ = fan.GetRpmAvg()
		_ = fan.GetMinPwm()
		_ = fan.GetStartPwm()
		_ = fan.GetMaxPwm()
		fan.SetMinPwm(10+i%5, true)
		_ = fan.GetConfig()
		_ = fan.GetCurveId()
		_ = fan.ShouldNeverStop()
		_ = fan.Supports(FeatureControlModeRead)
		_ = fan.Supports(FeaturePwmSensor)
		_ = fan.Supports(FeatureRpmSensor)
		_, _ = fan.GetControlMode()
		_ = fan.GetFanRpmCurveData()
		_ = fan.AttachFanRpmCurveData(&map[int]float64{0: 0, 100: 1000, 255: 2000})
		fan.UpdateFanRpmCurveValue(128, 1200)
		_, err := json.Marshal(fan)
		assert.NoError(t, err)
	}
}

func runActorsConcurrently(t *testing.T, fan Fan, iterations int) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		runRpmActor(fan, iterations)
	}()
	go func() {
		defer wg.Done()
		runUpdateActor(t, fan, iterations)
	}()
	wg.Wait()
}

func writeIntFile(t *testing.T, path string, value string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(value), 0644))
}

func TestHwMonFan_ConcurrentAccess(t *testing.T) {
	// GIVEN
	dir := t.TempDir()
	rpmPath := filepath.Join(dir, "fan1_input")
	pwmPath := filepath.Join(dir, "pwm1")
	pwmEnablePath := filepath.Join(dir, "pwm1_enable")
	writeIntFile(t, rpmPath, "1200")
	writeIntFile(t, pwmPath, "100")
	writeIntFile(t, pwmEnablePath, "2")

	fan, err := NewFan(configuration.FanConfig{
		ID:    "hwmon",
		Curve: "curve",
		HwMon: &configuration.HwMonFanConfig{
			Index:         1,
			RpmInputPath:  rpmPath,
			PwmPath:       pwmPath,
			PwmEnablePath: pwmEnablePath,
		},
	})
	require.NoError(t, err)

	// WHEN / THEN (no race reported)
	runActorsConcurrently(t, fan, concurrencyIterations)
}

// SetControlMode stores the last known automatic control mode and the driver specific mode
// fan2go wrote, while GetControlMode (called by the update loop) reads and writes them.
func TestHwMonFan_ConcurrentControlModeAccess(t *testing.T) {
	// GIVEN
	dir := t.TempDir()
	pwmEnablePath := filepath.Join(dir, "pwm1_enable")
	writeIntFile(t, pwmEnablePath, "2")

	fan, err := NewFan(configuration.FanConfig{
		ID: "hwmon",
		HwMon: &configuration.HwMonFanConfig{
			Index:         1,
			PwmEnablePath: pwmEnablePath,
		},
	})
	require.NoError(t, err)

	// WHEN / THEN (no race reported)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < concurrencyIterations; i++ {
			_ = fan.SetControlMode(ControlModeAutomatic)
			// a driver specific mode, which the fan remembers
			_ = fan.SetControlMode(ControlMode(5))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < concurrencyIterations; i++ {
			_, _ = fan.GetControlMode()
			_ = fan.Supports(FeatureControlModeRead)
			_, _ = json.Marshal(fan)
		}
	}()
	wg.Wait()
}

func TestFileFan_ConcurrentAccess(t *testing.T) {
	// GIVEN
	dir := t.TempDir()
	rpmPath := filepath.Join(dir, "rpm")
	pwmPath := filepath.Join(dir, "pwm")
	writeIntFile(t, rpmPath, "1200")
	writeIntFile(t, pwmPath, "100")

	fan, err := NewFan(configuration.FanConfig{
		ID:    "file",
		Curve: "curve",
		File: &configuration.FileFanConfig{
			Path:    pwmPath,
			RpmPath: rpmPath,
		},
	})
	require.NoError(t, err)

	// WHEN / THEN (no race reported)
	runActorsConcurrently(t, fan, concurrencyIterations)
}

func TestCmdFan_ConcurrentAccess(t *testing.T) {
	// GIVEN
	echo := getEchoPath()
	fan, err := NewFan(configuration.FanConfig{
		ID:    "cmd",
		Curve: "curve",
		Cmd: &configuration.CmdFanConfig{
			SetPwm: &configuration.ExecConfig{Exec: echo, Args: []string{"%pwm%"}},
			GetPwm: &configuration.ExecConfig{Exec: echo, Args: []string{"100"}},
			GetRpm: &configuration.ExecConfig{Exec: echo, Args: []string{"1200"}},
		},
	})
	require.NoError(t, err)

	// WHEN / THEN (no race reported)
	// commands are slow, so fewer iterations are used
	runActorsConcurrently(t, fan, 20)
}

// The first Supports(FeatureControlModeRead) call probes pwm_enable and caches the result.
func TestHwMonFan_ConcurrentFirstSupportsCall(t *testing.T) {
	dir := t.TempDir()
	pwmEnablePath := filepath.Join(dir, "pwm1_enable")
	writeIntFile(t, pwmEnablePath, "1")

	for i := 0; i < 50; i++ {
		// GIVEN
		fan, err := NewFan(configuration.FanConfig{
			ID:    "hwmon",
			HwMon: &configuration.HwMonFanConfig{PwmEnablePath: pwmEnablePath},
		})
		require.NoError(t, err)

		// WHEN
		var wg sync.WaitGroup
		results := make([]bool, 2)
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				results[g] = fan.Supports(FeatureControlModeRead)
			}(g)
		}
		wg.Wait()

		// THEN (no race reported)
		assert.Equal(t, []bool{true, true}, results)
	}
}

// GetFanRpmCurveData returns a copy, so callers can't modify (or read) the fan's data
// while it is updated under the fan's lock.
func TestHwMonFan_GetFanRpmCurveData_ReturnsCopy(t *testing.T) {
	// GIVEN
	fan := &HwMonFan{}
	original := map[int]float64{0: 0, 255: 2000}
	require.NoError(t, fan.AttachFanRpmCurveData(&original))

	// WHEN
	data := fan.GetFanRpmCurveData()
	(*data)[128] = 1000
	original[64] = 500

	// THEN
	assert.Equal(t, &map[int]float64{0: 0, 255: 2000}, fan.GetFanRpmCurveData())
}

// The update loop disables sanity checks at runtime while the RPM monitor, the statistics
// collector and the REST API read the fan config.
func TestFans_ConcurrentDisableSanityCheck(t *testing.T) {
	for _, config := range []configuration.FanConfig{
		{ID: "hwmon", HwMon: &configuration.HwMonFanConfig{RpmInputPath: "/nonexistent/fan1_input"}},
		{ID: "file", File: &configuration.FileFanConfig{Path: "/nonexistent/pwm"}},
		{ID: "cmd", Cmd: &configuration.CmdFanConfig{}},
	} {
		t.Run(config.ID, func(t *testing.T) {
			// GIVEN
			fan, err := NewFan(config)
			require.NoError(t, err)

			// WHEN
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				for i := 0; i < concurrencyIterations; i++ {
					fan.DisableSanityCheck(SanityCheckPwmValueChangedByThirdParty)
					fan.DisableSanityCheck(SanityCheckFanModeChangedByThirdParty)
				}
			}()
			go func() {
				defer wg.Done()
				for i := 0; i < concurrencyIterations; i++ {
					_ = fan.GetConfig()
					_, _ = fan.GetRpm()
					_, err := json.Marshal(fan)
					assert.NoError(t, err)
				}
			}()
			wg.Wait()

			// THEN
			disabled := configuration.Optional[bool]{Value: false, RuntimeOverride: true}
			sanityCheck := fan.GetConfig().SanityCheck
			assert.Equal(t, disabled, sanityCheck.PwmValueChangedByThirdParty.Enabled.Optional)
			assert.Equal(t, disabled, sanityCheck.FanModeChangedByThirdParty.Enabled.Optional)
		})
	}
}

// Copies of the config handed out before are not changed by DisableSanityCheck.
func TestHwMonFan_DisableSanityCheck_DoesNotChangeEarlierConfigCopies(t *testing.T) {
	// GIVEN
	fan, err := NewFan(configuration.FanConfig{ID: "hwmon", HwMon: &configuration.HwMonFanConfig{}})
	require.NoError(t, err)
	before := fan.GetConfig()

	// WHEN
	fan.DisableSanityCheck(SanityCheckPwmValueChangedByThirdParty)

	// THEN
	after := fan.GetConfig()
	assert.True(t, before.SanityCheck.PwmValueChangedByThirdParty.Enabled.Get())
	assert.False(t, after.SanityCheck.PwmValueChangedByThirdParty.Enabled.Get())
	assert.True(t, after.SanityCheck.FanModeChangedByThirdParty.Enabled.Get())
}
