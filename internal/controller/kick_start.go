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
// a fan needs more power to start than to keep spinning (static friction). Every time the update loop sets a target
// that should make a stopped fan spin (after a stopping target, or at the first update of a run), it supervises the
// start until the RPM readings show the fan spinning. If the fan stands still, it first sets a higher PWM value (the
// kick), hands the fan back to its target once it spins, and keeps watching for a short while. A kick that doesn't
// start the fan is raised (mode auto).

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
	// a kick PWM value raised at runtime is lowered by half a step again (never to a value that failed in this run)
	kickStartStepDownAfter = 5
	// kickStartJudgeReadings is the number of RPM readings in a row (taken after the fan could react) that must show
	// the fan spinning to end a kick, or standing still to count a start as failed
	kickStartJudgeReadings = 2
	// minKickStartTimeout is the shortest time after which a kick counts as failed
	minKickStartTimeout = 4 * time.Second
	// defaultTachInterval is the tach update interval if the analysis didn't measure it
	defaultTachInterval = time.Second
	// defaultSpinUpDelay is the time a fan needs from a write to a spinning RPM reading when it starts from
	// standstill, if the analysis didn't measure it
	defaultSpinUpDelay = 2 * time.Second
	// maxKickTimingValue caps the measured tach interval and spin-up delay (e.g. corrupt data)
	maxKickTimingValue = 10 * time.Second
	// defaultTimedKickDuration is the time a kick of a fan whose RPM can't be checked is held, if
	// kickStart.duration isn't set
	defaultTimedKickDuration = 3 * time.Second
)

// kickPhase is the phase of the supervision of a start from standstill.
type kickPhase int

const (
	// kickPhaseNone: no start is supervised
	kickPhaseNone kickPhase = iota
	// kickPhaseKicking: the kick value is set, waiting for the fan to spin
	kickPhaseKicking
	// kickPhaseWatching: the target is set (after a start without a kick, or after a kick), watching that the fan
	// spins until the window ends
	kickPhaseWatching
)

// kickStartState is the state of the kick start of the fan. Only used by the update loop.
type kickStartState struct {
	// phase of the supervised start, and the time of the write that started it
	phase kickPhase
	since time.Time
	// until is the end of the window of kickPhaseWatching
	until time.Time
	// pending is the phase that starts once the write of this update succeeded (see kickStartAfterWrite), and
	// pendingLevel the kick value of a pending kick
	pending      kickPhase
	pendingLevel int
	// lastSeq is the last RPM reading (rpmReadingCount) the supervision looked at
	lastSeq uint64
	// spinReadings and stopReadings count the RPM readings in a row, taken after the fan could react to the write
	// of the phase, that show the fan spinning / standing still; freshReadings counts all of them
	spinReadings  int
	stopReadings  int
	freshReadings int
	// kicked is true once the current start got a kick, kickLevel is the value of the last kick
	kicked    bool
	kickLevel int
	// attempt is the number of kicks that failed in the current start from standstill
	attempt int
	// level is the kick PWM value (pre-pwmMap) of mode auto, 0 until it was determined in this run; baseLevel is the
	// first value of the run, and levelSource describes where it comes from
	level       int
	baseLevel   int
	levelSource string
	levelLogged bool
	// failedLevel is the highest kick value that failed in this run (0: none): the step down never returns to it
	failedLevel int
	// successStreak counts the kicks in a row that started the fan at the first attempt
	successStreak int
	// disabled is set when even a kick at the highest PWM value didn't start the fan (mode auto): it isn't kicked
	// again by this controller (it survives an error restart, not a reload)
	disabled bool
	// cantKickLogged is set once it was logged that mode auto can't kick the fan (no working RPM sensor, no value)
	cantKickLogged bool
	// fixedTooLowLogged is set once it was logged that kickStart.pwm of mode fixed isn't above the target
	fixedTooLowLogged bool
}

// kickStart decides how the fan starts from standstill in this update. It returns the target to set instead of
// speedTarget (pre-pwmMap) and true while the fan is kicked, and false if speedTarget is to be set as usual (also
// while a start is watched). UpdateFanSpeed calls kickStartAfterWrite after the write.
func (f *DefaultFanController) kickStart(speedTarget int) (target int, kicking bool) {
	config := f.fan.GetConfig().GetKickStart()
	f.kick.pending = kickPhaseNone
	if config.Mode == configuration.KickStartOff {
		f.abortKickStart()
		return 0, false
	}
	now := f.getClock().Now()
	if !f.targetSpinsFan(speedTarget) {
		// the fan is supposed to stop: nothing to supervise
		if f.kick.phase != kickPhaseNone {
			f.finishKickStart(now)
		}
		return 0, false
	}
	transition := f.lastTarget == nil || !f.targetSpinsFan(*f.lastTarget)

	if !f.kickStartCanCheckRpm() {
		return f.timedKickStart(config, speedTarget, transition, now)
	}
	switch f.kick.phase {
	case kickPhaseKicking:
		return f.superviseKick(config, speedTarget, now)
	case kickPhaseWatching:
		return f.superviseStart(config, speedTarget, now)
	}
	if !transition || f.kick.disabled {
		return 0, false
	}
	return f.beginStartSupervision(config, speedTarget)
}

// beginStartSupervision starts the supervision of a start from standstill: a kick right away if a fresh RPM reading
// shows the fan standing still and the kick value is above the target, else the target, watched for a while (the
// reading may be stale or show a coasting fan, or the target may not be enough to start the fan).
func (f *DefaultFanController) beginStartSupervision(config configuration.KickStartConfig, speedTarget int) (int, bool) {
	f.kick.kicked = false
	f.kick.kickLevel = 0
	f.kick.attempt = 0
	// a single reading can be wrong (e.g. another program accesses the chip at the same time): kick right away only
	// if a fresh reading and the last reading of the RPM monitor both show the fan standing still. Otherwise the
	// watch decides, with kickStartJudgeReadings readings in a row.
	standstill := false
	if rpm, err := f.fan.GetRpm(); err == nil {
		standstill = !fans.IsRpmLikelySpinning(float64(rpm)) && f.rpmReadingCount.Load() > 0 && !f.rpmReadFailing.Load() &&
			!fans.IsRpmLikelySpinning(float64(f.lastRpmReading.Load()))
	} else {
		ui.Debug("Fan %s: can't read its RPM to check for standstill, watching its start: %v", f.fan.GetId(), err)
	}
	if level := f.kickStartLevel(config); standstill && level > speedTarget {
		f.kick.pending = kickPhaseKicking
		f.kick.pendingLevel = level
		return level, true
	}
	f.kick.pending = kickPhaseWatching
	return 0, false
}

// superviseKick handles an update while the fan is kicked: it hands the fan back to its target once
// kickStartJudgeReadings readings showed it spinning, and counts the kick as failed after kickStartTimeout.
func (f *DefaultFanController) superviseKick(config configuration.KickStartConfig, speedTarget int, now time.Time) (int, bool) {
	f.consumeKickStartReading(f.kick.since.Add(f.readbackDelay))
	if f.kick.spinReadings >= kickStartJudgeReadings {
		ui.Debug("Fan %s: started from standstill with a kick at PWM %d, setting its target %d", f.fan.GetId(),
			f.kick.kickLevel, speedTarget)
		// the target may not be enough to keep the fan accelerating: watch it for a while
		f.kick.pending = kickPhaseWatching
		return 0, false
	}
	timeout := f.kickStartTimeout(config)
	// no judgement without fresh readings (e.g. while reading the RPM fails)
	if now.Sub(f.kick.since) < timeout || f.kick.freshReadings < kickStartJudgeReadings || f.rpmReadFailing.Load() {
		return max(f.kick.kickLevel, speedTarget), true
	}
	f.kickStartFailed(f.kick.kickLevel, timeout)
	f.kick.since = now
	return f.escalateKickStart(config, speedTarget, now)
}

// superviseStart handles an update while a start is watched: kickStartJudgeReadings readings in a row that show the
// fan standing still (taken after it could react) mean that the start failed: the fan is kicked (again). Once the
// window ended, the start succeeded.
func (f *DefaultFanController) superviseStart(config configuration.KickStartConfig, speedTarget int, now time.Time) (int, bool) {
	f.consumeKickStartReading(f.kick.since.Add(f.kickStartResponseDelay()))
	if f.kick.stopReadings >= kickStartJudgeReadings && !f.rpmReadFailing.Load() {
		if f.kick.kicked {
			// handed back too early: the fan broke free, but the target couldn't keep it accelerating
			f.kickStartFailed(f.kick.kickLevel, now.Sub(f.kick.since))
		} else {
			ui.Debug("Fan %s doesn't spin at its target %d after a start from standstill", f.fan.GetId(), speedTarget)
		}
		f.kick.since = now
		return f.escalateKickStart(config, speedTarget, now)
	}
	if now.Before(f.kick.until) {
		return 0, false
	}
	if f.kick.kicked {
		f.kickStartSucceeded(config, f.kick.kickLevel)
	}
	f.finishKickStart(now)
	return 0, false
}

// escalateKickStart kicks the fan (again) after a failed start: at the kick value if there was no kick yet and it is
// above the target, else (mode auto) one step above the last kick or the target, up to fans.FailSafeMaxPwm. If that
// isn't possible, it gives up: mode auto doesn't kick the fan again.
func (f *DefaultFanController) escalateKickStart(config configuration.KickStartConfig, speedTarget int, now time.Time) (int, bool) {
	fanId := f.fan.GetId()
	current := speedTarget
	if f.kick.kicked {
		current = max(f.kick.kickLevel, speedTarget)
	} else if level := f.kickStartLevel(config); level > speedTarget {
		f.kick.pending = kickPhaseKicking
		f.kick.pendingLevel = level
		return level, true
	}
	if config.Mode == configuration.KickStartFixed {
		if !f.kick.kicked {
			if !f.kick.fixedTooLowLogged {
				f.kick.fixedTooLowLogged = true
				ui.Warning("Fan %s doesn't start from standstill at its target %d, and kickStart.pwm (%d) isn't above it. "+
					"Raise kickStart.pwm", fanId, speedTarget, f.kickStartLevel(config))
			}
		} else {
			ui.Warning("Fan %s didn't start from standstill with a kick at PWM %d (kickStart.pwm). Raise kickStart.pwm "+
				"or kickStart.duration, or use kickStart.mode: auto", fanId, f.kick.kickLevel)
		}
		f.finishKickStart(now)
		return 0, false
	}
	next := f.nextKickStartLevel(current)
	if next <= current {
		f.kick.disabled = true
		ui.Warning("Fan %s didn't start from standstill, even with a kick at PWM %d (the highest kick). fan2go doesn't "+
			"kick it again until its config is reloaded or fan2go is restarted. Check the fan and its tach signal, or set "+
			"kickStart.mode: off for this fan", fanId, current)
		f.finishKickStart(now)
		return 0, false
	}
	if f.kick.kicked {
		ui.Info("Fan %s didn't start from standstill with a kick at PWM %d, kicking it at PWM %d", fanId, f.kick.kickLevel, next)
	} else {
		ui.Info("Fan %s didn't start from standstill at its target %d, kicking it at PWM %d", fanId, speedTarget, next)
	}
	f.kick.attempt++
	f.kick.pending = kickPhaseKicking
	f.kick.pendingLevel = next
	return next, true
}

// timedKickStart handles a fan whose RPM can't be checked: on a start from standstill (standstill is assumed), it
// is kicked at an explicitly configured value (kickStart.pwm, or a startPwm above minPwm) for kickStart.duration.
func (f *DefaultFanController) timedKickStart(config configuration.KickStartConfig, speedTarget int, transition bool, now time.Time) (int, bool) {
	if f.kick.phase == kickPhaseKicking {
		hold := config.Duration.Duration()
		if hold <= 0 {
			hold = defaultTimedKickDuration
		}
		hold = max(hold, configuration.MinKickStartDuration.Duration())
		if now.Sub(f.kick.since) >= hold {
			f.finishKickStart(now)
			return 0, false
		}
		return max(f.kick.kickLevel, speedTarget), true
	}
	if f.kick.phase != kickPhaseNone {
		// e.g. the RPM sensor was seen working before: end what it started
		f.finishKickStart(now)
	}
	if !transition {
		return 0, false
	}
	level, ok := f.explicitKickStartLevel(config)
	if !ok {
		if !f.kick.cantKickLogged {
			f.kick.cantKickLogged = true
			ui.Info("Fan %s: kickStart.mode: auto can't kick this fan from standstill: its RPM sensor is missing or "+
				"hasn't been seen working. Set kickStart.pwm, or kickStart.mode: fixed with kickStart.pwm, to kick it", f.fan.GetId())
		}
		return 0, false
	}
	if level <= speedTarget {
		return 0, false
	}
	f.kick.kicked = false
	f.kick.pending = kickPhaseKicking
	f.kick.pendingLevel = level
	return level, true
}

// kickStartAfterWrite starts the phase kickStart decided on for this update, if the write succeeded. A failed write
// changes nothing: the next update decides again.
func (f *DefaultFanController) kickStartAfterWrite(written bool) {
	pending := f.kick.pending
	f.kick.pending = kickPhaseNone
	if pending == kickPhaseNone || !written {
		return
	}
	now := f.getClock().Now()
	f.kick.phase = pending
	f.kick.since = now
	f.kick.lastSeq = f.rpmReadingCount.Load()
	f.kick.spinReadings = 0
	f.kick.stopReadings = 0
	f.kick.freshReadings = 0
	switch pending {
	case kickPhaseWatching:
		f.kick.until = now.Add(f.kickStartWatchWindow())
	case kickPhaseKicking:
		level := f.kick.pendingLevel
		f.kick.kicked = true
		f.kick.kickLevel = level
		if f.fan.GetConfig().GetKickStart().Mode == configuration.KickStartAuto && level > f.kick.level {
			// remembered for the next starts in this run
			f.kick.level = level
		}
		if !f.kick.levelLogged && f.kick.levelSource != "" {
			f.kick.levelLogged = true
			ui.Info("Fan %s: kicking the fan from standstill, at PWM %d first (%s)", f.fan.GetId(), f.kick.baseLevel, f.kick.levelSource)
		}
		f.statsMutex.Lock()
		f.stats.KickStartCount += 1
		f.stats.KickStartPwm = level
		f.statsMutex.Unlock()
		ui.Debug("Fan %s: kicking the fan from standstill at PWM %d", f.fan.GetId(), level)
	}
}

// kickStartSupervising returns true while a start from standstill is supervised (or starts with this update).
func (f *DefaultFanController) kickStartSupervising() bool {
	return f.kick.phase != kickPhaseNone || f.kick.pending != kickPhaseNone
}

// consumeKickStartReading counts the latest RPM reading of the RPM monitor, if it is new and was taken at or after
// judgeFrom (before that, the fan couldn't react to the write yet, or the tach reported an older value).
func (f *DefaultFanController) consumeKickStartReading(judgeFrom time.Time) {
	seq := f.rpmReadingCount.Load()
	if seq == f.kick.lastSeq {
		return
	}
	f.kick.lastSeq = seq
	at := time.Unix(0, f.lastRpmReadingAt.Load())
	if at.Before(judgeFrom) {
		return
	}
	f.kick.freshReadings++
	if fans.IsRpmLikelySpinning(float64(f.lastRpmReading.Load())) {
		f.kick.spinReadings++
		f.kick.stopReadings = 0
	} else {
		f.kick.stopReadings++
		f.kick.spinReadings = 0
	}
}

// finishKickStart ends the supervision: the stall checks give the fan their full reaction period from now on.
func (f *DefaultFanController) finishKickStart(now time.Time) {
	f.kick.phase = kickPhaseNone
	f.kick.pending = kickPhaseNone
	f.kick.attempt = 0
	f.restartStallReactionPeriod()
	f.pwmFloorSetAt = now
}

// abortKickStart ends the supervision without judging it, e.g. when the curve fails or kickStart is off.
func (f *DefaultFanController) abortKickStart() {
	f.kick.phase = kickPhaseNone
	f.kick.pending = kickPhaseNone
	f.kick.attempt = 0
}

// kickStartFailed records a kick that didn't start the fan.
func (f *DefaultFanController) kickStartFailed(level int, after time.Duration) {
	ui.Debug("Fan %s: the kick at PWM %d didn't start the fan (after %s)", f.fan.GetId(), level, after.Round(time.Millisecond))
	f.statsMutex.Lock()
	f.stats.FailedKickStartCount += 1
	f.statsMutex.Unlock()
	f.kick.successStreak = 0
	f.kick.failedLevel = max(f.kick.failedLevel, level)
}

// kickStartSucceeded records a kick that started the fan, and lowers a kick PWM value of mode auto that was raised
// at runtime again after kickStartStepDownAfter kicks in a row that started the fan at the first attempt. It never
// lowers it below the first value of the run, and never to (or within half a step above) a value that failed in
// this run.
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
	halfStep := f.kickStartStep() / 2
	lowered := max(f.kick.baseLevel, level-halfStep)
	if f.kick.failedLevel > 0 {
		// keep half a step of distance to a value that failed: a value just above it would fail again sooner or later
		lowered = max(lowered, f.kick.failedLevel+halfStep)
	}
	if lowered >= level {
		return
	}
	ui.Info("Fan %s started from standstill with a kick at PWM %d %d times in a row, kicking it at PWM %d from now on",
		f.fan.GetId(), level, kickStartStepDownAfter, lowered)
	f.kick.level = lowered
}

// kickStartCanCheckRpm returns true if the RPM of the fan shows whether it stands still and whether a kick started
// it: the fan has an RPM sensor, and it has been seen working (a measured RPM curve with a spinning point, or a
// spinning fan in this run). The curve data of file and cmd fans isn't measured, so it doesn't count.
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

// explicitKickStartLevel returns the kick value configured for the fan, and false if there is none:
// kickStart.pwm, or (mode auto) a configured startPwm above minPwm.
func (f *DefaultFanController) explicitKickStartLevel(config configuration.KickStartConfig) (int, bool) {
	highest := fans.FailSafeMaxPwm(f.fan)
	if config.Pwm != nil {
		return util.Coerce(*config.Pwm, 1, highest), true
	}
	if startPwm := f.fan.GetConfig().StartPwm; config.Mode == configuration.KickStartAuto && startPwm != nil &&
		*startPwm > f.fan.GetMinPwm() {
		return util.Coerce(*startPwm, 1, highest), true
	}
	return 0, false
}

// kickStartLevel returns the PWM value (pre-pwmMap) of the next kick: kickStart.pwm for mode fixed, the current
// level for mode auto (determined by kickStartBaseLevel at the first use in a run).
func (f *DefaultFanController) kickStartLevel(config configuration.KickStartConfig) int {
	highest := fans.FailSafeMaxPwm(f.fan)
	if config.Mode == configuration.KickStartFixed && config.Pwm != nil {
		return util.Coerce(*config.Pwm, 1, highest)
	}
	if f.kick.level == 0 {
		level, source := f.kickStartBaseLevel(config, highest)
		f.kick.baseLevel = level
		f.kick.level = level
		f.kick.levelSource = source
	}
	return f.kick.level
}

// kickStartBaseLevel returns the first kick PWM value (pre-pwmMap) of mode auto, at most highest, and where it
// comes from: kickStart.pwm, else a configured startPwm, else the breakaway PWM value measured by the analysis plus
// a margin, else startPwm plus a quarter of the range up to highest.
func (f *DefaultFanController) kickStartBaseLevel(config configuration.KickStartConfig, highest int) (level int, source string) {
	if config.Pwm != nil {
		level = *config.Pwm
		source = "kickStart.pwm"
	} else if startPwm := f.fan.GetConfig().StartPwm; startPwm != nil {
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

// nextKickStartLevel returns the kick PWM value one step above level, at least the next value that the pwmMap maps
// to another raw value, at most FailSafeMaxPwm. Returns level if it is the highest already.
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

// kickStartTachInterval returns the time between two RPM readings that can show a new value: the tach update
// interval measured by the analysis (defaultTachInterval if unknown), at least rpmPollingRate.
func (f *DefaultFanController) kickStartTachInterval() time.Duration {
	interval := f.tachInterval
	if interval <= 0 {
		interval = defaultTachInterval
	}
	return max(interval, configuration.CurrentConfig.RpmPollingRate)
}

// kickStartResponseDelay returns the time after a write until an RPM reading can show whether the fan started:
// readback delay + spin-up delay (measured, else defaultSpinUpDelay) + one tach interval.
func (f *DefaultFanController) kickStartResponseDelay() time.Duration {
	spinUp := f.spinUpDelay
	if spinUp <= 0 {
		spinUp = defaultSpinUpDelay
	}
	return f.readbackDelay + spinUp + f.kickStartTachInterval()
}

// kickStartWatchWindow returns how long a start is watched after the target was set: the response delay plus
// kickStartJudgeReadings tach intervals.
func (f *DefaultFanController) kickStartWatchWindow() time.Duration {
	return f.kickStartResponseDelay() + kickStartJudgeReadings*f.kickStartTachInterval()
}

// kickStartTimeout returns the time after which a kick that didn't start the fan counts as failed: the watch window,
// at least minKickStartTimeout and at least kickStart.duration.
func (f *DefaultFanController) kickStartTimeout(config configuration.KickStartConfig) time.Duration {
	return max(f.kickStartWatchWindow(), minKickStartTimeout, config.Duration.Duration())
}

// loadKickStartInfo loads the breakaway PWM value, the tach interval and the spin-up delay measured by the analysis
// of the fan (see persistence.FanAnalysisInfo). Values that weren't measured (e.g. data saved by an older version)
// or can't be read are unknown, and the kick start uses its defaults.
func (f *DefaultFanController) loadKickStartInfo() {
	fan := f.fan
	f.breakawayPwm = nil
	f.tachInterval = 0
	f.spinUpDelay = 0
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
	if info.BreakawayPwm != nil {
		breakaway := util.Coerce(*info.BreakawayPwm, fans.MinPwmValue, fans.MaxPwmValue)
		f.breakawayPwm = &breakaway
	}
	if info.TachIntervalMillis != nil {
		f.tachInterval = util.Coerce(time.Duration(*info.TachIntervalMillis)*time.Millisecond, 0, maxKickTimingValue)
	}
	if info.SpinUpMillis != nil {
		f.spinUpDelay = util.Coerce(time.Duration(*info.SpinUpMillis)*time.Millisecond, 0, maxKickTimingValue)
	}
	breakaway := "unknown"
	if f.breakawayPwm != nil {
		breakaway = fmt.Sprint(*f.breakawayPwm)
	}
	ui.Debug("Fan %s: measured kick start data: breakaway PWM %s, tach interval %s, spin-up delay %s",
		fan.GetId(), breakaway, f.tachInterval, f.spinUpDelay)
}

// durationMillis returns the given duration in milliseconds (rounded up), nil if it is nil.
func durationMillis(d *time.Duration) *int64 {
	if d == nil {
		return nil
	}
	millis := d.Milliseconds()
	if time.Duration(millis)*time.Millisecond < *d {
		millis++
	}
	return &millis
}

func ceilDiv(a int, b int) int {
	return (a + b - 1) / b
}
