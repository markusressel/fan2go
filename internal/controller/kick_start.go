package controller

import (
	"fmt"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

// Kick start from standstill (see docs/fan-controller.md, "Kick start from standstill"):
// a fan needs more power to start than to keep spinning (static friction). When the update loop sets a target that
// should make a fan spin, and the fan stands still, it first sets a higher PWM value (the kick) until the RPM sensor
// shows the fan spinning, then it hands the fan back to the normal target.

const (
	// kickStartStepDivisor: a kick that doesn't start the fan raises the kick PWM value of mode auto by
	// 1/kickStartStepDivisor of the full range (32 of 255, 13 of 100 for nvidia fans)
	kickStartStepDivisor = 8
	// kickStartMarginDivisor: mode auto kicks at the breakaway PWM value measured by the analysis plus
	// 1/kickStartMarginDivisor of the full range (8 of 255, 4 of 100), since starting can take more power on some days
	kickStartMarginDivisor = 32
	// kickStartFallbackDivisor: without a measured breakaway PWM value, mode auto kicks at startPwm plus
	// 1/kickStartFallbackDivisor of the range between startPwm and the highest kick PWM value
	kickStartFallbackDivisor = 4
	// kickStartStepDownAfter is the number of kicks in a row that started the fan at the first attempt after which
	// a kick PWM value raised at runtime is lowered by half a step again (never below the initial kick PWM value)
	kickStartStepDownAfter = 5
	// kickStartMinConfirmPeriods: a kick of a fan whose RPM is checked lasts at least this many RPM polling periods
	// before it counts as failed, so the RPM monitor can see the fan spin
	kickStartMinConfirmPeriods = 2
)

// kickStartState is the state of the kick start of the fan. Only used by the update loop.
type kickStartState struct {
	// active is true while the fan is kicked
	active bool
	// since is the time the current kick (attempt) started
	since time.Time
	// readingsAtStart is the number of RPM readings (rpmReadingCount) when the current kick started: only later
	// readings can show that the kick started the fan
	readingsAtStart uint64
	// attempt is the number of kicks that failed in the current start from standstill
	attempt int
	// level is the kick PWM value (pre-pwmMap) of mode auto, 0 until it was determined in this Run
	level int
	// baseLevel is the first kick PWM value of mode auto in this Run (see kickStartBaseLevel)
	baseLevel int
	// successStreak counts the kicks in a row that started the fan at the first attempt
	successStreak int
	// disabled is set when even a kick at the highest PWM value didn't start the fan (mode auto): it isn't kicked
	// again in this Run
	disabled bool
}

// kickStart decides whether the fan has to be kicked instead of getting speedTarget (pre-pwmMap) in this update.
// It returns the target to set instead and true while the fan is kicked, and false if speedTarget is to be set as usual.
// A kick starts when speedTarget should make the fan spin, the last target didn't (or there was none in this Run),
// and the fan stands still (by its RPM, or assumed for mode fixed if the RPM can't be checked). It ends once the RPM
// monitor saw the fan spinning, when the duration ran out, or when the target stops the fan.
func (f *DefaultFanController) kickStart(speedTarget int) (target int, kicking bool) {
	config := f.fan.GetConfig().GetKickStart()
	if config.Mode == configuration.KickStartOff {
		f.abortKickStart()
		return 0, false
	}
	now := f.getClock().Now()
	if f.kick.active {
		return f.continueKickStart(config, speedTarget, now)
	}

	if f.kick.disabled || !f.targetSpinsFan(speedTarget) {
		return 0, false
	}
	if f.lastTarget != nil && f.targetSpinsFan(*f.lastTarget) {
		// the fan was supposed to spin already: this isn't a start from standstill
		return 0, false
	}
	confirmable := f.kickStartCanCheckRpm()
	if !confirmable && config.Mode != configuration.KickStartFixed {
		// mode auto can neither see whether the fan stands still nor whether the kick started it
		return 0, false
	}
	if confirmable {
		rpm, err := f.fan.GetRpm()
		if err != nil {
			ui.Debug("Fan %s: not kicking the fan, its RPM can't be read: %v", f.fan.GetId(), err)
			return 0, false
		}
		if fans.IsRpmLikelySpinning(float64(rpm)) {
			return 0, false
		}
	}
	level := f.kickStartLevel(config)
	if level <= speedTarget {
		// the target itself is high enough to start the fan
		return 0, false
	}
	f.kick.attempt = 0
	f.startKickStart(level, speedTarget, now)
	return level, true
}

// continueKickStart handles an update while the fan is kicked, see kickStart.
func (f *DefaultFanController) continueKickStart(config configuration.KickStartConfig, speedTarget int, now time.Time) (int, bool) {
	fanId := f.fan.GetId()
	if !f.targetSpinsFan(speedTarget) {
		// the fan is supposed to stop now
		f.endKickStart(now)
		return 0, false
	}
	level := f.kickStartLevel(config)
	confirmable := f.kickStartCanCheckRpm()
	if confirmable && f.kickStartSpinSeen() {
		f.kickStartSucceeded(config, level)
		ui.Debug("Fan %s: started from standstill with a kick at PWM %d, setting its target %d", fanId, level, speedTarget)
		f.endKickStart(now)
		return 0, false
	}

	timeout := config.Duration.Duration()
	if confirmable {
		timeout = max(timeout, kickStartMinConfirmPeriods*configuration.CurrentConfig.RpmPollingRate)
	}
	if now.Sub(f.kick.since) < timeout {
		return max(level, speedTarget), true
	}
	if !confirmable {
		// held for kickStart.duration, the fan is assumed to spin now
		f.endKickStart(now)
		return 0, false
	}

	f.statsMutex.Lock()
	f.stats.FailedKickStartCount += 1
	f.statsMutex.Unlock()
	f.kick.successStreak = 0
	if config.Mode == configuration.KickStartAuto {
		if next := f.nextKickStartLevel(level); next > level {
			ui.Info("Fan %s didn't start from standstill with a kick at PWM %d within %s, kicking it at PWM %d",
				fanId, level, timeout, next)
			f.kick.level = next
			f.kick.attempt++
			f.startKickStart(next, speedTarget, now)
			return max(next, speedTarget), true
		}
		f.kick.disabled = true
		ui.Warning("Fan %s didn't start from standstill, even with a kick at PWM %d (the highest kick) for %s. "+
			"fan2go doesn't kick it again until it is restarted or its config is reloaded. Check the fan and its "+
			"tach signal, or set kickStart.mode: off for this fan", fanId, level, timeout)
	} else {
		ui.Warning("Fan %s didn't start from standstill with a kick at PWM %d (kickStart.pwm) within %s. "+
			"Raise kickStart.pwm or kickStart.duration, or use kickStart.mode: auto", fanId, level, timeout)
	}
	f.endKickStart(now)
	return 0, false
}

// startKickStart starts a kick (attempt) at the given level.
func (f *DefaultFanController) startKickStart(level int, speedTarget int, now time.Time) {
	f.kick.active = true
	f.kick.since = now
	f.kick.readingsAtStart = f.rpmReadingCount.Load()
	f.statsMutex.Lock()
	f.stats.KickStartCount += 1
	f.stats.KickStartPwm = level
	f.statsMutex.Unlock()
	ui.Debug("Fan %s: kicking the fan from standstill at PWM %d (target %d)", f.fan.GetId(), level, speedTarget)
}

// endKickStart ends a kick: the fan gets its normal target again, and the stall checks give it the full reaction
// period from now on.
func (f *DefaultFanController) endKickStart(now time.Time) {
	f.kick.active = false
	f.kick.attempt = 0
	f.restartStallReactionPeriod()
	f.pwmFloorSetAt = now
}

// abortKickStart ends a kick without judging it, e.g. when the curve fails or kickStart is off.
func (f *DefaultFanController) abortKickStart() {
	f.kick.active = false
	f.kick.attempt = 0
}

// kickStartSucceeded records a kick that started the fan, and lowers a kick PWM value of mode auto that was raised
// at runtime again after kickStartStepDownAfter kicks in a row that started the fan at the first attempt.
func (f *DefaultFanController) kickStartSucceeded(config configuration.KickStartConfig, level int) {
	if config.Mode != configuration.KickStartAuto || f.kick.attempt > 0 || level <= f.kick.baseLevel {
		f.kick.successStreak = 0
		return
	}
	f.kick.successStreak++
	if f.kick.successStreak < kickStartStepDownAfter {
		return
	}
	f.kick.successStreak = 0
	lowered := max(f.kick.baseLevel, level-f.kickStartStep()/2)
	ui.Info("Fan %s started from standstill with a kick at PWM %d %d times in a row, kicking it at PWM %d from now on",
		f.fan.GetId(), level, kickStartStepDownAfter, lowered)
	f.kick.level = lowered
	f.statsMutex.Lock()
	f.stats.KickStartPwm = lowered
	f.statsMutex.Unlock()
}

// kickStartSpinSeen returns true if an RPM reading taken after the current kick started shows the fan spinning.
func (f *DefaultFanController) kickStartSpinSeen() bool {
	// the RPM monitor stores the reading before it counts it
	if f.rpmReadingCount.Load() <= f.kick.readingsAtStart {
		return false
	}
	return fans.IsRpmLikelySpinning(float64(f.lastRpmReading.Load()))
}

// kickStartCanCheckRpm returns true if the RPM of the fan shows whether it stands still and whether a kick started
// it: the fan has an RPM sensor, and it has been seen working (a measured RPM curve with a spinning point, or a
// spinning fan in this Run). The curve data of file and cmd fans isn't measured, so it doesn't count.
func (f *DefaultFanController) kickStartCanCheckRpm() bool {
	if !f.fan.Supports(fans.FeatureRpmSensor) {
		return false
	}
	if f.rpmSpinSeen.Load() {
		return true
	}
	if config := f.fan.GetConfig(); config.HwMon == nil && config.Nvidia == nil {
		return false
	}
	return f.tachIsCredible()
}

// targetSpinsFan returns true if the given target (pre-pwmMap) should make the fan spin: it is above 0 and the
// pwmMap doesn't map it to 0.
func (f *DefaultFanController) targetSpinsFan(target int) bool {
	return target > 0 && f.applyPwmMapToTarget(target) > 0
}

// kickStartLevel returns the PWM value (pre-pwmMap) of the next kick: kickStart.pwm for mode fixed, the current
// level for mode auto (determined by kickStartBaseLevel at the first kick of a Run).
func (f *DefaultFanController) kickStartLevel(config configuration.KickStartConfig) int {
	highest := fans.FailSafeMaxPwm(f.fan)
	if config.Mode == configuration.KickStartFixed && config.Pwm != nil {
		return util.Coerce(*config.Pwm, 1, highest)
	}
	if f.kick.level == 0 {
		level, source := f.kickStartBaseLevel(highest)
		f.kick.baseLevel = level
		f.kick.level = level
		ui.Info("Fan %s: kicks from standstill use PWM %d (%s)", f.fan.GetId(), level, source)
	}
	return f.kick.level
}

// kickStartBaseLevel returns the first kick PWM value (pre-pwmMap) of mode auto, at most highest, and where it
// comes from: a configured startPwm, else the breakaway PWM value measured by the analysis plus a margin, else
// startPwm plus a quarter of the range up to highest.
func (f *DefaultFanController) kickStartBaseLevel(highest int) (level int, source string) {
	if startPwm := f.fan.GetConfig().StartPwm; startPwm != nil {
		level = *startPwm
		source = "the configured startPwm"
	} else if f.breakawayPwm != nil {
		margin := ceilDiv(fans.FullRangeMaxPwm(f.fan), kickStartMarginDivisor)
		level = *f.breakawayPwm + margin
		source = fmt.Sprintf("the breakaway PWM value %d measured by the fan analysis plus a margin of %d",
			*f.breakawayPwm, margin)
	} else {
		derivedStartPwm := f.fan.GetStartPwm()
		level = derivedStartPwm + max(0, highest-derivedStartPwm)/kickStartFallbackDivisor
		source = fmt.Sprintf("startPwm %d plus a quarter of the range up to %d", derivedStartPwm, highest)
		if config := f.fan.GetConfig(); config.HwMon != nil || config.Nvidia != nil {
			source += fmt.Sprintf(": the breakaway PWM value wasn't measured, run 'fan2go fan --id %s init' to "+
				"measure it", f.fan.GetId())
		}
	}
	return util.Coerce(level, 1, highest), source
}

// kickStartStep returns the step by which a failed kick raises the kick PWM value of mode auto.
func (f *DefaultFanController) kickStartStep() int {
	return ceilDiv(fans.FullRangeMaxPwm(f.fan), kickStartStepDivisor)
}

// nextKickStartLevel returns the kick PWM value after a kick at level failed: one step higher, at least to the next
// value that the pwmMap maps to another raw value, at most FailSafeMaxPwm. Returns level if it is the highest already.
func (f *DefaultFanController) nextKickStartLevel(level int) int {
	highest := fans.FailSafeMaxPwm(f.fan)
	if level >= highest {
		return level
	}
	next := min(level+f.kickStartStep(), highest)
	for next < highest && f.applyPwmMapToTarget(next) == f.applyPwmMapToTarget(level) {
		next++
	}
	return next
}

// loadBreakawayPwm loads the breakaway PWM value measured by the analysis of the fan (see
// persistence.FanAnalysisInfo). It is nil if none was measured (e.g. data saved by an older version) or the info
// can't be read.
func (f *DefaultFanController) loadBreakawayPwm() {
	fan := f.fan
	f.breakawayPwm = nil
	if config := fan.GetConfig(); config.HwMon == nil && config.Nvidia == nil {
		return
	}
	info, err := f.persistence.LoadFanAnalysisInfo(fan.GetId())
	if err != nil {
		if !persistence.IsMissingOrCorrupt(err) {
			ui.Warning("Fan %s: unable to load the measured breakaway PWM value: %v", fan.GetId(), err)
		}
		return
	}
	if info.BreakawayPwm == nil {
		return
	}
	breakaway := util.Coerce(*info.BreakawayPwm, fans.MinPwmValue, fans.MaxPwmValue)
	f.breakawayPwm = &breakaway
	ui.Debug("Fan %s: breakaway PWM value measured by the fan analysis: %d", fan.GetId(), breakaway)
}

func ceilDiv(a int, b int) int {
	return (a + b - 1) / b
}
