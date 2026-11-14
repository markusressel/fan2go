package controller

import (
	"strings"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// glitchTestCacheFor is the time the glitchTestFan caches its readings, like the it87 driver
const glitchTestCacheFor = 1500 * time.Millisecond

// glitchTestFan is a MockFan whose PWM readings are cached like by the it87 driver (glitchTestCacheFor), and whose
// next driver updates can return garbage (pwmGlitches) instead of the real value. Its control mode reads return
// modeGlitches first. It records the PWM and control mode writes. Only for single goroutine tests.
type glitchTestFan struct {
	MockFan
	clock util.Clock

	// values returned by the next driver updates of the PWM value instead of the real one
	pwmGlitches []int
	cacheValid  bool
	cachedPwm   int
	refreshedAt time.Time

	// values returned by the next control mode reads instead of the real one
	modeGlitches []fans.ControlMode

	writes     []int
	modeWrites []fans.ControlMode
}

func (fan *glitchTestFan) GetPwm() (int, error) {
	now := fan.clock.Now()
	if !fan.cacheValid || now.Sub(fan.refreshedAt) >= glitchTestCacheFor {
		if len(fan.pwmGlitches) > 0 {
			fan.cachedPwm = fan.pwmGlitches[0]
			fan.pwmGlitches = fan.pwmGlitches[1:]
		} else {
			fan.cachedPwm, _ = fan.MockFan.GetPwm()
		}
		fan.refreshedAt = now
		fan.cacheValid = true
	}
	return fan.cachedPwm, nil
}

func (fan *glitchTestFan) SetPwm(pwm int) error {
	fan.writes = append(fan.writes, pwm)
	// the driver updates its cache on a write
	fan.cacheValid = false
	return fan.MockFan.SetPwm(pwm)
}

func (fan *glitchTestFan) GetControlMode() (fans.ControlMode, error) {
	if len(fan.modeGlitches) > 0 {
		mode := fan.modeGlitches[0]
		fan.modeGlitches = fan.modeGlitches[1:]
		return mode, nil
	}
	return fan.MockFan.GetControlMode()
}

func (fan *glitchTestFan) SetControlMode(value fans.ControlMode) error {
	fan.modeWrites = append(fan.modeWrites, value)
	return fan.MockFan.SetControlMode(value)
}

// setThirdPartyPwm sets the PWM value of the fan like a third party would (no write recorded)
func (fan *glitchTestFan) setThirdPartyPwm(pwm int) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	fan.PWM = pwm
}

type glitchTest struct {
	fan        *glitchTestFan
	controller *DefaultFanController
	clock      *util.FakeClock
	curveValue *float64
}

// newGlitchTest returns a controller (identity pwmMap, unscaled curve values) that set its glitchTestFan to 100,
// with a fake clock.
func newGlitchTest(t *testing.T) *glitchTest {
	t.Helper()
	clock := util.NewFakeClock(time.Unix(1000, 0))
	fan := &glitchTestFan{
		MockFan: MockFan{
			ID:                     "fan",
			ControlMode:            fans.ControlModePWM,
			MaxPWM:                 255,
			useUnscaledCurveValues: true,
			sanityCheckFanModeChangedByThirdPartyEnabled: true,
		},
		clock: clock,
	}
	curveValue := 100.0
	controller := newIdentityMappedController(fan, 0)
	controller.curve = MockCurve{ID: "curve", Value: &curveValue}
	controller.SetClock(clock)
	g := &glitchTest{fan: fan, controller: controller, clock: clock, curveValue: &curveValue}
	g.update(t)
	require.Equal(t, []int{100}, fan.writes)
	// the readback of the write has settled
	clock.Advance(2 * time.Second)
	return g
}

// update runs one update of the controller, and returns what it logged. The update must not wait on the
// (fake) clock: it would never return.
func (g *glitchTest) update(t *testing.T) string {
	t.Helper()
	return captureLog(t, func() {
		require.NoError(t, g.controller.UpdateFanSpeed())
	})
}

// updateEvery runs n updates, one every interval, and returns what they logged.
func (g *glitchTest) updateEvery(t *testing.T, interval time.Duration, n int) string {
	t.Helper()
	var log strings.Builder
	for i := 0; i < n; i++ {
		g.clock.Advance(interval)
		log.WriteString(g.update(t))
	}
	return log.String()
}

const thirdPartyWarning = "was changed by third party"

func TestThirdPartyPwmCheck_SingleGlitch_NoWarningNoWrite(t *testing.T) {
	// GIVEN
	g := newGlitchTest(t)

	// WHEN one driver update returns garbage, and the controller keeps updating every 500ms
	g.fan.pwmGlitches = []int{46}
	log := g.updateEvery(t, 500*time.Millisecond, 8)

	// THEN it is counted as a glitch: no warning, no write
	assert.NotContains(t, log, thirdPartyWarning)
	assert.Equal(t, []int{100}, g.fan.writes)
	stats := g.controller.GetStatistics()
	assert.Equal(t, 1, stats.PwmReadGlitchCount)
	assert.Equal(t, 0, stats.UnexpectedPwmValueCount)
	assert.Equal(t, 0, g.controller.thirdPartyPwmChanges.count)
	assert.False(t, g.controller.pendingPwmMismatch.pending())
}

func TestThirdPartyPwmCheck_NoReadBeforeTheConfirmationDelay(t *testing.T) {
	// GIVEN a glitch found by the check
	g := newGlitchTest(t)
	g.fan.pwmGlitches = []int{46}
	g.updateEvery(t, 500*time.Millisecond, 1)
	require.True(t, g.controller.pendingPwmMismatch.pending())

	// WHEN the controller updates until just before the confirmation delay
	g.updateEvery(t, minThirdPartyConfirmationDelay/4, 3)
	g.clock.Advance(minThirdPartyConfirmationDelay/4 - time.Millisecond)
	g.update(t)

	// THEN it is still pending, and nothing was written
	assert.True(t, g.controller.pendingPwmMismatch.pending())
	assert.Equal(t, []int{100}, g.fan.writes)

	// WHEN the delay has passed
	g.clock.Advance(time.Millisecond)
	g.update(t)

	// THEN
	assert.False(t, g.controller.pendingPwmMismatch.pending())
	assert.Equal(t, 1, g.controller.GetStatistics().PwmReadGlitchCount)
}

func TestThirdPartyPwmCheck_PersistentChange_WarnsAndRestoresAfterTheDelay(t *testing.T) {
	// GIVEN
	g := newGlitchTest(t)

	// WHEN a third party sets another value, which the next update reads
	g.fan.setThirdPartyPwm(80)
	log := g.updateEvery(t, 500*time.Millisecond, 1)

	// THEN nothing happens yet
	assert.NotContains(t, log, thirdPartyWarning)
	assert.Equal(t, []int{100}, g.fan.writes)

	// WHEN the confirmation delay has passed
	log = g.updateEvery(t, 500*time.Millisecond, 4)

	// THEN it is reported and restored, like before
	assert.Equal(t, 1, strings.Count(log, thirdPartyWarning), log)
	assert.Contains(t, log, "expected reported pwm '100' but was '80'")
	assert.Equal(t, []int{100, 100}, g.fan.writes)
	stats := g.controller.GetStatistics()
	assert.Equal(t, 1, stats.UnexpectedPwmValueCount)
	assert.Equal(t, 0, stats.PwmReadGlitchCount)

	// WHEN the next check reads the restored value
	log = g.updateEvery(t, 2*time.Second, 1)

	// THEN the recovery is logged
	assert.Contains(t, log, "PWM of fan matches the value set by fan2go again, after 1 unexpected values in a row")
}

func TestThirdPartyPwmCheck_GlitchThenTargetChange(t *testing.T) {
	// GIVEN a glitch found by the check
	g := newGlitchTest(t)
	g.fan.pwmGlitches = []int{46}
	g.updateEvery(t, 500*time.Millisecond, 1)
	require.True(t, g.controller.pendingPwmMismatch.pending())

	// WHEN the target changes while the mismatch is pending
	*g.curveValue = 150
	log := g.updateEvery(t, 500*time.Millisecond, 6)

	// THEN the new target is written as usual, and the pending mismatch is neither a glitch nor a third party change
	assert.Equal(t, []int{100, 150}, g.fan.writes)
	assert.NotContains(t, log, thirdPartyWarning)
	stats := g.controller.GetStatistics()
	assert.Equal(t, 0, stats.PwmReadGlitchCount)
	assert.Equal(t, 0, stats.UnexpectedPwmValueCount)
	assert.False(t, g.controller.pendingPwmMismatch.pending())
}

func TestThirdPartyPwmCheck_ChangeThatPersistsAfterATargetChange_IsReported(t *testing.T) {
	// GIVEN a third party change found by the check
	g := newGlitchTest(t)
	g.fan.setThirdPartyPwm(80)
	g.updateEvery(t, 500*time.Millisecond, 1)

	// WHEN the target changes while it is pending, and the third party sets its value again
	*g.curveValue = 150
	g.updateEvery(t, 500*time.Millisecond, 1)
	require.Equal(t, []int{100, 150}, g.fan.writes)
	g.fan.setThirdPartyPwm(80)
	log := g.updateEvery(t, 500*time.Millisecond, 4)

	// THEN the second read is compared with the new target: a third party change
	assert.Equal(t, 1, strings.Count(log, thirdPartyWarning), log)
	assert.Contains(t, log, "expected reported pwm '150' but was '80'")
	assert.Equal(t, []int{100, 150, 150}, g.fan.writes)
}

func TestThirdPartyModeCheck_SingleGlitch_NoWarningNoWrite(t *testing.T) {
	// GIVEN
	g := newGlitchTest(t)

	// WHEN one control mode read returns garbage
	g.fan.modeGlitches = []fans.ControlMode{fans.ControlModeDisabled}
	log := g.updateEvery(t, 500*time.Millisecond, 8)

	// THEN it is counted as a glitch, and the mode isn't written
	assert.NotContains(t, log, "Fan mode of fan")
	assert.Empty(t, g.fan.modeWrites)
	assert.Equal(t, 1, g.controller.GetStatistics().ControlModeReadGlitchCount)
	assert.False(t, g.controller.pendingModeMismatch.pending())
}

func TestThirdPartyModeCheck_RealChange_IsRestoredAfterTheDelay(t *testing.T) {
	// GIVEN
	g := newGlitchTest(t)

	// WHEN a third party switches the fan to automatic mode
	require.NoError(t, g.fan.MockFan.SetControlMode(fans.ControlModeAutomatic))
	g.updateEvery(t, 500*time.Millisecond, 4)

	// THEN it is not restored before the confirmation delay
	assert.Empty(t, g.fan.modeWrites)

	// WHEN the delay has passed
	g.updateEvery(t, 500*time.Millisecond, 1)

	// THEN
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM}, g.fan.modeWrites)
	assert.Equal(t, 0, g.controller.GetStatistics().ControlModeReadGlitchCount)
}

// pwm_enable 0 at pwm 255 is manual mode on it87 chips without fan on/off control: no mismatch to confirm.
func TestThirdPartyModeCheck_FullDutyQuirk_IsNoMismatch(t *testing.T) {
	// GIVEN
	fan, pwmEnablePath := newFullDutyHwMonFan(t, "it8665", true)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := DefaultFanController{fan: fan, clock: clock}

	// WHEN
	controller.ensureFanModeIsSetToExpectedMode()

	// THEN
	assert.False(t, controller.pendingModeMismatch.pending())
	assert.Equal(t, 0, controller.GetStatistics().ControlModeReadGlitchCount)
	assert.Equal(t, "0", readPwmEnableFile(t, pwmEnablePath))
}

func TestPendingConfirmation_Observe(t *testing.T) {
	now := time.Unix(1000, 0)
	delay := 2 * time.Second
	tests := []struct {
		name     string
		first    bool
		write    bool
		second   bool
		expected confirmationResult
	}{
		{"glitch", false, false, true, confirmationGlitch},
		{"overwritten", false, true, true, confirmationOverwritten},
		{"confirmed", false, false, false, confirmationConfirmed},
		{"confirmed after a write", false, true, false, confirmationConfirmed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p pendingConfirmation
			assert.Equal(t, confirmationStarted, p.observe(now, delay, tt.first))
			assert.True(t, p.waiting(now.Add(delay-time.Nanosecond)))
			assert.False(t, p.waiting(now.Add(delay)))
			if tt.write {
				p.noteWrite()
			}
			assert.Equal(t, tt.expected, p.observe(now.Add(delay), delay, tt.second))
			assert.False(t, p.pending())
		})
	}

	// a match without a pending mismatch, and a write without one
	var p pendingConfirmation
	p.noteWrite()
	assert.Equal(t, confirmationMatch, p.observe(now, delay, true))
	assert.False(t, p.pending())
}
