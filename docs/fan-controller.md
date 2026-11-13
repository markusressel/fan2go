# Fan controller (runtime)

fan2go runs one fan controller per configured fan. It takes the value of the fan's curve, runs it through the
fan's control algorithm, maps the result to a PWM value the hardware accepts, writes it, and watches the fan for
stalls, foreign changes and failures. When it stops, it hands the fan back. The code lives in
`internal/controller/controller.go` (`DefaultFanController`) and the fan backends in `internal/fans`
(`HwMonFan`, `NvidiaFan`, `FileFan`, `CmdFan`). The daemon wiring is in `internal/daemon.go` and `internal/factory.go`.

This document covers the controller after the fan is set up. The initial fan analysis, the detection of the
`pwmMap` and `setPwmToGetPwmMap` and their persistence are described in [fan-analysis.md](fan-analysis.md).
The control algorithms (`direct`, the default with a time constant and a deadband, and `pid`) are described in [control-algorithms.md](control-algorithms.md), sensor
polling and staleness in [sensors.md](sensors.md), and curves and the curve evaluator in [curves.md](curves.md).

## How it works

```
                      every fanController.adjustmentTickRate (200ms)
  CurveEvaluator ── evaluates all curves once ── notify (buffered chan, size 1) ──┐
                                                                                  v
 Run(ctx)                                                         update loop goroutine
  prepareController, storeInitialFanState                           UpdateFanSpeed():
  wait startupSensorWarmup (2s) + tempSensorPollingRate               curve.CurrentResult()
  runInitializationIfNeeded / load curve data / pwmMap                  error -> onSensorFailure (max | restore)
  takeManualControl (controlMode.active)                              controlLoop.Cycle(now, value)
  run.Group:                                                          clamp, scale to [minPwm..maxPwm]
    RPM monitor goroutine (every rpmPollingRate)                      neverStop floor + stall detection
      measureRpm -> exponential moving average                        setTargetPwm -> pwmMap -> fan.SetPwm
    update loop goroutine (startupUpdateDelay 1s, then per cycle)     kick start from standstill (kickStart)
                                                                      checkStall (fans without neverStop)
                                                                      handlePwmWriteResult
  restoreControlModeOnce on every exit path
```

Main types and state:

- `FanController` (interface, used by the daemon, statistics and CLI), implemented by `DefaultFanController`.
- `FanStateSnapshot` holds the control mode and raw PWM value the fan had before fan2go touched it, and for hwmon
  fans the driver value behind "automatic" (e.g. `pwm_enable=5`). All are pointers; `nil` means "unknown". While
  fan2go controls the fan, the snapshot is also saved in the database (see below).
- `lastTarget` is the last target fan2go wrote, before the `pwmMap`. `lastSetPwm` is the raw value after the
  `pwmMap`, exposed to statistics through `GetLastSetPwm`.
- `minPwmOffset`, `pwmFloorSetAt`, `rpmAvgSeeded` (stall detection); `stallCheckSince`, `stallCheckTarget`,
  `stallReported`, `lastRpmReading`, `rpmSpinSeen`, `rpmReadFailing` (stall check of fans without `neverStop`); `kick`,
  `rpmSample`, `breakawayPwm`, `tachInterval`, `spinUpDelay`, `coastDown` (kick start from standstill); `sensorFailureAction`, `pwmWriteFailures`,
  `thirdPartyPwmChanges` (failure states of the update loop).
- `clock` (`util.Clock`) is the time source for all waits, tickers and timestamps.

Startup timing: `Run` waits `startupSensorWarmup` (2 s) plus one `tempSensorPollingRate`, so the sensor monitors
have taken a few readings before the fan is touched (each sensor is already seeded once at startup, see
[sensors.md](sensors.md)). A sensor that keeps failing is still stale after the warm-up. After the setup, the
update loop waits `startupUpdateDelay` (1 s), then updates the fan once per curve evaluator cycle. With the
defaults, a fan whose analysis is saved is controlled about 3.2 s after start or reload.

## Design decisions

### The update loop is driven by the curve evaluator, not by its own ticker

**Decision**: A central `curves.CurveEvaluator` evaluates every curve once per `fanController.adjustmentTickRate`
and then notifies each controller through a buffered channel of size 1 (`SetCurveCycles`, `Subscribe`). The
update loop reads the stored result with `CurrentResult()` and never evaluates a curve. Only a controller without a
subscription (tests) falls back to its own ticker at `updateRate`. If the evaluator has not evaluated the curve
yet (`curves.ErrCurveNotEvaluated`), the first update waits for the first cycle instead of treating it as a failure.
The evaluator itself is described in [curves.md](curves.md).

**Why**: Before #553, each controller evaluated its curve itself on its own ticker. A curve shared by several fans
was evaluated several times per cycle, which was a data race on stateful curves (PID, staircase) and stepped a
shared PID curve once per fan. Fans that share a curve also acted on values from different moments.

**Who benefits**: Users with curves shared between fans or inside `function` curves, and `pid` curve users.

**Alternatives considered**: An event-driven pipeline and per-fan tick rates were left out of scope (#553).

**Trade-offs**: All fans update at the same global rate. The notification channel drops a notification while the
controller still handles the previous one, so a slow fan (e.g. a `cmd` fan with slow commands) skips cycles
instead of queueing them. The update loop still selects on its own context, so stopping and restoring a fan does
not depend on the evaluator, which the daemon stops only after all controllers (`runOrchestratorCycle`).

### Injectable clock and context-aware waits

**Decision**: All time in the controller goes through `util.Clock` (`SetClock`, default `util.RealClock`). Waits
use `sleepWithContext`, tickers use `clock.NewTicker`. `startupSensorWarmup` and `startupUpdateDelay` are fields
with the defaults 2 s and 1 s. `internal/controller` has no `time.Sleep`.

**Why**: Tests used to sleep for seconds and raced with real tickers, and waits that ignored the context delayed
shutdown (#542). The stall detection (#547) and the PWM write failure timeout (#552) build on the clock. If `Run` is
cancelled during the warm-up, it returns without touching the fan. **Who benefits**: maintainers (fast,
deterministic tests under `-race`); users (shutdown isn't delayed by waits).

**Trade-offs**: The startup delays are not user-configurable. The controller still reads
`configuration.CurrentConfig` directly instead of getting its config passed in (kept on purpose in #542).

### From curve value to PWM value

**Decision**: `UpdateFanSpeed` turns the control loop output into a raw PWM value in fixed steps:

1. Clamp the output to `[0..255]` (logs a warning if it was out of range).
2. Scale it, unless `useUnscaledCurveValues` is set:
   - output `< 1` and no `neverStop`: PWM target 0 (the fan may stop).
   - otherwise output `[1..255]` is mapped linearly to `[minPwm..maxPwm]`
     (`minPwm + round((output-1)/254 * (maxPwm-minPwm))`). With `neverStop`, an output below 1 is treated like 1.
3. With `useUnscaledCurveValues`, the output is used as is, but a value between 1 and `minPwm` becomes 0, because
   the fan would not spin at that value anyway.
4. With `neverStop`, the target is raised to at least `minPwm + minPwmOffset` (see stall detection).
5. If the fan has to start from standstill, the kick start replaces the target with a higher one for a while
   (see [Kick start from standstill](#kick-start-from-standstill)).
6. `setTargetPwm` maps the target through the `pwmMap` (`applyPwmMapToTarget`) and writes the raw value.

`minPwm`, `startPwm` and `maxPwm` come from the config if set, otherwise from the saved fan analysis
(`AttachFanRpmCurveData`). Without config values, `minPwm` is set to the measured `startPwm`. `startPwm` itself
is not used by the update loop: it is only logged, and `Run` warns if `minPwm > startPwm`. If a derived `minPwm` or
`startPwm` ends up above `maxPwm` (e.g. `maxPwm: 100` and a measured start of 120), `AttachFanRpmCurveData` logs a
warning and clamps (`clampDerivedPwmBoundaries`), so the scaling can't run backwards. A configured value always
wins: with a configured `maxPwm`, the derived `minPwm`/`startPwm` are lowered to it; otherwise the derived `maxPwm`
is raised to the (configured or derived) `minPwm`/`startPwm` (a higher PWM is the safe direction), as far as the
fan allows (nvidia: 100); derived values still above it are lowered. After the PWM mappings are known, `Run` raises a
derived `maxPwm` to the first output level that reaches 98% of the peak RPM
(`alignDerivedMaxPwmWithOutputLevels`, see [fan-analysis.md](fan-analysis.md)). The derived `maxPwm` is only the
top of the scaling: the fail-safe paths (`onSensorFailure: max` and the `neverStop` stall check) use the configured
`maxPwm`, or full speed (255; nvidia: 100) without one (`fans.FailSafeMaxPwm`). A fan that has to start from
standstill gets a kick first (see [Kick start from standstill](#kick-start-from-standstill)). File and cmd fans always report `minPwm` 0 and `maxPwm` 255; nvidia
fans default to `maxPwm` 100, because NVML speeds are percent.

**Why**: Curves describe the cooling demand on a fixed `0..255` (or `0..100%`) scale, independent of the fan.
The controller scales that to the range in which the fan actually changes speed, so 1 % means "slowest spinning
speed" and 100 % means "the fastest useful speed". The `pwmMap` then handles drivers that accept only some values
or a different range ([fan-analysis.md](fan-analysis.md)). `useUnscaledCurveValues` exists for users who want to
write exact PWM values from the curve.

**Who benefits**: Users who write curves in percent for fans with different or odd PWM ranges.

**Trade-offs**: Three value domains exist side by side: the curve/control loop output, the pre-`pwmMap` target
(`lastTarget`, `onExit.speed`) and the raw value (`lastSetPwm`, `FanStateSnapshot.PwmValue`). Mixing them up
caused bugs before (#541, #543), and the code comments now mark the domain of each value. The `minPwm` the fan
uses after an analysis is really its start PWM, which is higher than needed to keep a spinning fan turning.

### Skip writes that would not change anything, and check what the fan reports

**Decision**: If the fan can report its PWM (`FeaturePwmSensor`), `setTargetPwm` reads it first and skips the
write when the fan already reports the value the write would produce (looked up in `setPwmToGetPwmMap`).
`lastTarget` and `lastSetPwm` are only updated after a successful write or a skipped write.

Before each normal update, `ensureNoThirdPartyIsMessingWithUs` compares the reported PWM with the value expected
for `lastTarget`. A mismatch counts in `UnexpectedPwmValueCount` (metric
`fan2go_controller_unexpected_pwm_value_count`) every time, but the warning is only logged for the first
mismatch, every 25th in a row and the recovery. fan2go does not fight back here; it overwrites the value with
its own target on the same update anyway.

**Why**: Another program, the BIOS or a driver with a "virtual" PWM can change the value behind fan2go's back
(#64). Updating `lastTarget` only after a successful write keeps a failed write from being reported as a third
party change (#552). Rate-limiting replaced a warning on every cycle, which flooded the log (#552, #64).

**Who benefits**: Users debugging competing fan control software or drivers whose readback differs (AMD GPUs).

**Trade-offs**: Each update reads the PWM twice (check and skip test); for `cmd` fans, two `getPwm` runs per cycle.

### Sanity checks switch themselves off when they cannot work

**Decision**: `sanityCheck.pwmValueChangedByThirdParty` needs a PWM readback, and
`sanityCheck.fanModeChangedByThirdParty` needs a readable control mode. If the fan lacks the feature, the
controller logs a warning once and calls `fan.DisableSanityCheck`, which sets a runtime override in the fan
config under the fan lock. The REST API shows the effective value, `"enabled": false`.

`fanModeChangedByThirdParty` reads the control mode at most once per `throttleDuration` (default 10 s). If the
mode is not manual PWM, it calls `trySetManualPwm` again, which writes `controlMode.active`. This check also runs
inside `setTargetPwm`, so it covers the max PWM writes during a sensor failure.

**Why**: Some BIOSes and tools switch `pwm_enable` back to automatic while fan2go runs. The throttle avoids a
`pwm_enable` read and write every cycle. `DisableSanityCheck` replaced `fan.SetConfig(...)`, which swapped the whole
config while the RPM monitor read it (#544).

**Who benefits**: Users whose BIOS or vendor tools take back fan control; `cmd`/`file` fan users (no mode read).

**Trade-offs**: A config reload creates new fan objects, so the override and its warning come back after each
reload. The check always expects manual PWM mode. With `controlMode.active` set to anything other than `pwm`, it
re-applies the configured mode on every check (harmless, but needless writes; noted in #543).

### Control modes: take manual control, no fallback

**Decision**: `takeManualControl` writes `controlMode.active` (default `pwm`) through `fan.SetControlMode`. There
is no fallback to another mode: if the write fails and the fan doesn't read back as manual PWM anyway, the error
(`fan2go can't control the fan: can't switch fan X to control mode pwm (1): <cause>`) is returned. `Run` then stops
with state `stopped_error` and that error (status, `fan2go_controller_state`, `/controller`), hands the fan back
(see below), and the daemon warns and notifies; other fans keep running. If the write fails but the fan reads back
as manual PWM (e.g. a read-only `pwm_enable` that is 1 already), the error is ignored. When the recovery after
`onSensorFailure: restore` can't take manual control again, the fan stays handed back (see "Controller state"). The fan analysis and the
set→get PWM sweep take manual control the same way and fail with that error instead of measuring a fan that fan2go
doesn't control. Fans that cannot write a control mode (file, cmd, hwmon without `pwm_enable`) skip this step. If
the fan has a [known hardware quirks](hardware-quirks.md) entry with a hint (e.g. `thinkpad_acpi fan_control=1`), the
hint is added to the error.

On hwmon, `SetControlMode` reads `pwm_enable` back to verify it. Any value `>= 2` counts as automatic; the last
automatic value seen (e.g. 5 on nct6775) is remembered and written when fan2go hands the fan back to automatic.
Integer `active` values `>= 3` are written as is. A write-only `pwm_enable` (e.g. `dell_smm_hwmon`, #229) is
accepted without verification. Many `it87` chips report manual mode at `pwm` 255 as `pwm_enable` 0; for them, the
[`it87` known hardware quirks entry](hardware-quirks.md#it87-pwm_enable-0-at-full-duty) makes 0 with `pwm` 255 count
as manual mode (also in the `fanModeChangedByThirdParty` sanity check).

**Why**: fan2go cannot set the speed unless the driver is in manual mode. Up to 0.16.0, a failed write of `pwm`
fell back to `disabled` (`pwm_enable=0`). That fallback came with the first working version (2021, 052cb34) without a
documented reason, and no issue names a device that needs it. Per the kernel hwmon docs and driver sources, `0` is
not supported by `dell_smm_hwmon`, `f71882fg` (`EINVAL`) and `it87` on IT8603E/IT8623E, so the fallback only
replaced the real error with a second one. On most other drivers (e.g. `nct6775`, other `it87` chips) `0` means full
speed, and PWM writes have no effect: the controller kept running and reported the fan as controlled while it ran at full speed. A
controller that can't control its fan must say so.

**Who benefits**: Users whose fan can't be switched to manual mode (missing permissions, `thinkpad_acpi` without
`fan_control=1`): they see the real error, the status shows that fan2go doesn't control the fan, and the fan
stays in its original mode instead of running at full speed. Drivers that reject `0` (`dell_smm_hwmon`, `f71882fg`)
log only the real error.

**Alternatives considered**: Keeping the fallback for drivers where `0` is known to work: on all of them `0` is
full speed without PWM control, which is not "controlled" either. Users who want that can set
`controlMode.active: disabled` explicitly.

**Trade-offs**: On a driver that rejects `1` but accepts `0`, the fan used to run at full speed; now it is handed
back to its original (usually automatic) mode and fan2go doesn't control it. This is a breaking change.

### Snapshot the original state, restore it exactly once

**Decision**: Before touching the fan, `Run` stores a `FanStateSnapshot` (`storeInitialFanState`). Only values
that were read successfully are stored. On every way out of `Run` (cancel, reload, error, panic, stall), the fan is
restored through `restoreControlMode`, which is the single place that resets a fan. A `sync.Once` that `Run`
creates anew on every call (`restoreOnce`) makes sure the normal stop, an update error and a panic restore only
once. What `restoreControlMode` does depends on `controlMode.onExit`:

| `onExit`              | Control mode written                                    | PWM written                                   |
|-----------------------|---------------------------------------------------------|-----------------------------------------------|
| unset or `restore`    | original mode (if known)                                | original raw PWM, unless the mode is automatic |
| `none`                | nothing                                                 | nothing                                        |
| `mode` and/or `speed` | `mode` if set; else the original mode unless it was auto | `speed` through the `pwmMap`; else original PWM unless handing back to auto |

Special cases:

- Original mode unknown, but writable (e.g. write-only `pwm_enable`), default `restore`: switch to automatic and
  write no PWM, because on some drivers a PWM write switches back to manual. Only if that fails, set manual mode at
  the original PWM, if known (`restoreUnknownControlMode`).
- Writing the control mode fails: write the mapped value of 255 instead (full speed).
- The original PWM is unknown (no readback, read failed): write no PWM; the fan stays at the last speed fan2go set.
- `onExit.speed` and the full-speed fallback go through the `pwmMap`. Before the `pwmMap` exists (stop during
  initialization), the value is written unmapped (`applyPwmMapForRestore`).
- Errors before fan2go may have written to the fan (`mayHaveWrittenToFan`) don't restore anything, so a fan fan2go
  never touched is not changed by an explicit `onExit.speed`.

**Why**: Leaving a fan in manual mode at the last PWM value after fan2go exits is unsafe: the fan does not react
to temperature anymore. The old snapshot made up values it could not read (PWM 0, mode `disabled`), and restoring
them stopped fans for good (#543). `onExit: none` exists for hardware controllers that remember their last setting
and would otherwise jump to full speed (#416).

**Who benefits**: Everyone who stops or reloads fan2go; fans without PWM readback (no made-up PWM 0 on exit).

**Alternatives considered**: Restoring the PWM before the control mode (#127, like `fancontrol`); instead, the
PWM is only written when the fan is not handed back to automatic.

**Trade-offs**: Fans whose state could not be read keep their last speed on exit, which may be low (set
`onExit.speed`). The restore is best effort: failures are logged, not retried.

### Persist the original state until the fan is handed back

**Decision**: The snapshot is also saved in the bbolt database (bucket `fanOriginalState`, keyed by fan id, see
[fan-analysis.md](fan-analysis.md)) while fan2go controls the fan (`internal/controller/original_state.go`):

- Saved right before fan2go may write to the fan (`markMayWriteToFan`: start of the initialization, the set -> get
  PWM sweep, `takeManualControl` in `Run`, and taking control again after `onSensorFailure: restore`). A `Run` that
  stops during the warm-up saves nothing.
- Deleted after a complete restore (`restoreControlModeOnceAndForget`): no write failed, no fallback to full speed,
  no panic. This covers exit, reload, update errors, panics, `fan2go fan init` and `onSensorFailure: restore` if
  the fan was handed over. `onExit: none` counts as complete. If `restore` falls back to `max`, fan2go still controls
  the fan and the entry stays. After an incomplete restore, the entry stays too, and the stopped controller reports
  the error "fan not handed back completely, see the log" in its status (`/controller`).
- On start, `storeInitialFanState` reads the fan as before, then loads the entry. A saved state wins over the
  read one (logged as a warning): the fan is presumably still in the state a killed run left behind. The hwmon
  driver value of the automatic mode is seeded into the fan (`fans.AutomaticControlModeKeeper`), so the restore
  writes e.g. 5 and not 2. Because the fan is not in its original state, the controller also restores it on errors
  or a stop before it touched the fan (`handbackPending`).
- The entry stores a hardware key built from stable config values (hwmon `platform/index/pwmChannel`, nvidia
  `device/index`, file `path`, cmd `setPwm` command), not from resolved sysfs paths. An entry whose key doesn't match
  the fan config is logged, deleted, and the read state is used. Editing these values (e.g. the `platform` regex)
  without changing the hardware also drops the entry.
- Corrupt entries (unparsable, PWM outside 0..255, control mode other than 0, 1, 2) are deleted and ignored.
- `controlMode.onExit.controlMode` that can't be parsed counts as an incomplete restore. A fan that can't write
  its control mode counts as complete (nothing more can be done).
- Entries of fans that are no longer configured are logged once and deleted at daemon start (see
  [daemon.md](daemon.md)).
- Database errors never stop a controller. A load error (e.g. the database is locked): the read state is used,
  and this controller neither overwrites nor deletes the entry, so a later run can still use it. The load is retried
  once per `Run`, right before fan2go takes over the fan. A save error: fan2go
  takes control anyway (as before). A delete error: the next run restores to the same state again. These operations
  wait at most 5 s for the database lock, not 1 minute.

**Why**: Before, the snapshot lived in memory only. After SIGKILL or an OOM kill (`MemoryMax=256M` in the unit),
the fan stays in manual mode at the last PWM; systemd restarts fan2go, which took that state as "original" and
"restored" manual mode at a fixed PWM on its next exit. The same happened on reload when the old controller's
restore was incomplete: the new controller snapshotted fan2go's own state. An entry that only exists while fan2go
controls the fan is a precise marker for "not handed back".

**Who benefits**: Users running fan2go under systemd (OOM kills, crashes, hard restarts); hwmon users with a
driver specific automatic mode.

**Alternatives considered**: Treating manual mode at startup as unknown and handing the fan back to automatic
control (wrong for users who keep a fan in manual mode on purpose). A boot id check, so that entries from before a
reboot are ignored (rejected: a warm reboot can keep manual mode, and the saved state is still the one from before
fan2go touched the fan). An `ExecStopPost` helper that restores without a new start (not implemented).

**Trade-offs**: A fan left behind by a killed run is only restored when fan2go stops the next time, not right
after the kill. If the user changes the fan state between the kill and the next start, the saved state still wins.
Without a working database, the behaviour is the old one. A fan whose restore always fails (e.g. a driver that
rejects automatic mode) keeps its entry, so every start logs the "didn't hand the fan back completely" warning; the
saved state is still its true original. `fan2go fan init` refuses to run while the daemon uses the same database
(see [fan-analysis.md](fan-analysis.md)), so it can't use and delete the daemon's entry.

### Panics, signals and hanging commands must not skip the restore

**Decision**:

- `Run`, the RPM monitor and the update loop recover panics, log them with a stack trace and return them as
  errors. The update loop restores right away, because it is the only goroutine that writes to the fan. A panic in
  the RPM monitor stops the update loop first, and `Run` restores after `g.Run()` returns, so an in-flight
  `SetPwm` can't land after the restore. A panic inside the restore is logged too.
- On an update error, the fan is restored first and the user notified afterwards (`stopAfterUpdateError`), so a
  hanging notification (D-Bus, `systemd-run`) cannot delay the restore.
- The signal handler keeps its subscription until the process exits. Further SIGINT/SIGTERM/SIGHUP during shutdown
  are logged and ignored (`ignoreSignalsDuringShutdown`). Only SIGKILL forces an exit.
- `cmd` fan commands have a timeout (`timeout`, default 2 s). On timeout, the whole process group is killed, and
  a 500 ms wait delay bounds waiting for output held open by background processes.
- A failing controller only stops itself. The daemon logs and notifies ("Fan Controller: <id>"), and other fans
  stay controlled.

**Why**: Before #548, a second Ctrl-C crashed the process (`panic: send on closed channel`), a non-executable
`cmd` crashed the daemon, a background process could block a command (and the restore) forever, and any panic
left all fans in manual mode.

**Who benefits**: People running fan2go headless under systemd; `cmd` fan and sensor users.

**Trade-offs**: SIGKILL, OOM kills and power loss still leave the fan at its last PWM until the next run restores
it on its exit (see above). Ctrl-C twice still waits for the restore. A stopped controller stays stopped until restart or reload.

### Sensor and curve failures: `max` by default, `restore` on request

**Decision**: A curve that can't be evaluated does not stop the controller. Causes: a stale sensor (no successful
reading for `sensorStaleTimeout`, or never read; see [sensors.md](sensors.md)), a sensor that was skipped at startup
or reload ("sensor not found", until the next reload), a failed sub-curve, or a panic in the curve.
`handleCurveFailure` applies `onSensorFailure` and returns `ErrCurveEvaluation`, which the update loop ignores; the
next cycle tries again.

- `max` (default): write the fail-safe speed (the configured `maxPwm`, else 255, nvidia: 100; through the `pwmMap`) on every
  cycle while the failure lasts. The `maxPwm` derived from the curve is not used: it can be below full speed. A failed write is
  logged once per failure state, and it does not count towards the PWM write timeout.
- `restore`: hand the fan back once, with the same logic as on exit. It only counts as success if the fan ends up
  in a control mode other than manual PWM (`restoreControlMode` returns `handedOver`). Otherwise (`onExit: none`,
  only `onExit.speed`, original mode manual, mode unknown or not writable), the fan is set to the fail-safe speed like `max`.
- Recovery (`recoverFromCurveFailure`): after `restore`, save the original state again (`markMayWriteToFan`), arm a
  new `restoreOnce` and take manual control again (like at startup). If that fails, the fan stays handed back, the
  state stays `sensor_failure_restored` with the error, and fan2go tries again on every cycle. The first failed
  attempt is warned about and notified once; the following ones are only logged at debug level. Only after manual
  control was taken again (logged once), a later failed attempt notifies again; a new curve failure in between
  doesn't (its error replaces the one in the state). `restoreOnce` is armed before the attempt, because the failed
  write may have changed the fan anyway. A stop while the fan stays handed back therefore runs the usual restore
  (see "Snapshot the original state"): it writes the original mode (or `controlMode.onExit`) again, falls back to
  max speed if that fails, and deletes the saved original state if the restore is complete, instead of leaving it
  in the database.
  Before that, `calculateTargetSpeed` makes the control loop continue from the fan's speed: `Preset` at the curve
  value 255 (with `useUnscaledCurveValues`: the fail-safe speed) after `max`, `Reset` (start at the target) after `restore`. So the fan comes down from max
  (with the time constant or ramps of the control algorithm) instead of jumping (see [control-algorithms.md](control-algorithms.md)).
- Entering the failure state is logged and notified once, after the fan is in a safe state. Recovery is logged
  once, and so is a recovery that can't take manual control again (see above). The stall detection is paused while the curve fails, because `UpdateFanSpeed` returns before it.

**Why**: Before #551, a sensor failure did one of three things depending on the curve type: stop the controller
(PID), keep the last value forever (linear, staircase), or treat a read error as 0 °C (file sensor). None was
fail-safe. Full speed is the safe default: loud, but it can't overheat anything. `restore` exists for users whose
BIOS has a sensible automatic curve.

**Who benefits**: Users with flaky `cmd` sensors, GPUs that power down, removed disks, unloadable drivers.

**Alternatives considered**: Keeping the last speed (not safe if the temperature rises); handing back with a fixed
PWM value (not fail-safe, so `restore` falls back to `max`). A hysteresis for flapping sensors is not implemented
(#551).

**Trade-offs**: `max` is loud. A sensor that flaps between stale and fresh notifies on every flap and, with
`restore`, switches the control mode back and forth.

### Persistent PWM write failures: restore and stop after 10 s and 3 attempts

**Decision**: `handlePwmWriteResult` counts consecutive failed writes (`consecutiveFailures`). It logs the first
failure, every 25th and the recovery. If writes keep failing for `pwmWriteFailureTimeout` (10 s, on the
controller's clock) and for at least `pwmWriteFailureMinAttempts` (3) attempts, `UpdateFanSpeed` returns
`ErrPwmWriteFailed`. The update loop then restores the fan once (like on exit), notifies the user and stops. A
successful write resets the series. Leaving the sensor failure state also resets it.

**Why**: Before #552, a failed write was logged every cycle and ignored forever. The fan stayed at whatever speed
it had, and `onSensorFailure: max` can't help when writes fail. Restoring hands the fan to a control path that may
still work (e.g. the BIOS). The minimum of 3 attempts keeps a single write that blocks for a long time (a slow
`cmd` fan) from stopping the controller.

**Who benefits**: Users whose driver gets unloaded, whose device disappears, or who have permission problems.

**Trade-offs**: The thresholds are constants, not config options. A short outage (> 10 s) stops control of the
fan until restart or reload; there is no automatic retry. The restore itself may fail for the same reason the
writes failed.

### `neverStop` stall detection

**Decision**: For a fan with `neverStop: true` and an RPM sensor, the update loop treats the fan as stalled when
its RPM average is below 50 RPM (`fans.IsRpmLikelySpinning`) while the target is unchanged since the last update
and the average has been seeded. It then raises `minPwmOffset` by 1 and the target by 1. After the first update
and after each raise, the fan gets 3 `rpmPollingRate` periods to react (`minPwmIncreaseRpmPollingPeriods`,
`hadTimeToReactToPwmFloor`). The floor can rise above a derived `maxPwm`. If the fan is still stalled at the
fail-safe speed (the configured `maxPwm`, else 255; nvidia: 100), `UpdateFanSpeed` returns
`ErrFanStalledAtMaxPwm`, and the controller restores the fan and stops. The offset is in the statistics
(`MinPwmOffset`, `IncreasedMinPwmCount`), but it is not persisted. Without an RPM sensor, `neverStop` only sets
the PWM floor, and fan2go logs a warning at startup. A fan that stands still when the controller starts is kicked
first (see [Kick start from standstill](#kick-start-from-standstill)); the detection is paused during the kick and
gives the fan its reaction period after it, so the offset is only raised if the kicks didn't start the fan.

**Why**: The old check was `avg <= 0`. An exponential moving average never reaches 0 from a positive value, so
the detection never fired for hwmon and nvidia fans, while file and cmd fans reacted to a single 0 reading (#547).
The reaction period replaced a hack (`SetRpmAvg(1)`) that overwrote the measured value, and it keeps a fan that
starts from standstill (zero-RPM mode) from being bumped at every start or reload.

**Who benefits**: Users who rely on `neverStop` to keep a CPU fan or pump spinning.

**Trade-offs**: A fan whose tach signal reports 0 RPM although it spins ends up at `maxPwm` and then uncontrolled;
fan2go logs a hint to check the tach or disable `neverStop`. Detection is slow on purpose: from 1000 RPM with the
defaults it takes 29 s until the average is below 50 RPM, plus 3 s per step. The check only runs while the target
is constant, so a target that alternates between two values (e.g. PID jitter) mostly skips it (#547). The default
control algorithm (low-pass plus deadband) keeps the target constant while the curve value jitters by less than
1 %, so the check runs; a ramp or a low-pass pauses it while the target changes. The floor `minPwm + minPwmOffset`
is applied after the control algorithm, so smoothing can't push a fan below it. The step is
1 in the pre-`pwmMap` domain, so with a coarse `pwmMap` many steps may not change the raw value at all.

### Stall check for fans without `neverStop`

**Decision**: For a fan without `neverStop` that has an RPM sensor, `checkStall` (called by `UpdateFanSpeed` after
the write) warns and notifies once when the fan doesn't spin although it should. All of these must hold:

- the target of the update (before the `pwmMap`) is at least `max(minPwm, 1)`, it was written successfully, and
  the `pwmMap` doesn't map it to 0;
- the fan had 3 `rpmPollingRate` periods to react to the target (`stallCheckSince`, the same reaction period as
  for `neverStop`). The period starts when the target reaches that range, and again on every change of the target
  to or from `max(minPwm, 1)`. A target that changes above it doesn't restart it: the fan was spinning before. It
  also restarts after a curve failure (the fan may have been handed back), when the PWM value was changed by a
  third party (`pwmValueChangedByThirdParty`), and when the control mode had to be set again
  (`fanModeChangedByThirdParty`): something else may have stopped the fan;
- the RPM average is seeded and below 50 RPM (`fans.IsRpmLikelySpinning`), and so is the last RPM reading, and
  the last RPM reading didn't fail (the average is frozen while it fails);
- the tach is credible: the fan's curve data has a point with at least 50 RPM, or the RPM monitor read at least
  50 RPM in this run of the controller (`rpmSpinSeen`).

The warning names the RPM, the target, `minPwm` and how long the fan had to react, and suggests what to check.
It is reported once per stall (`stallReported`) and counted in `StallCount` (metric
`fan2go_controller_stall_count`). When the RPM average is at least 50 RPM again, fan2go logs the recovery at INFO;
the next stall is reported again. A target of 0 (or below `minPwm`) in between doesn't end the stall, because fan2go
can't see whether the fan would spin. The check changes neither the PWM value nor the controller state. It is
paused while the curve fails (`UpdateFanSpeed` returns before it) and starts over on every run (reload): a stall
that lasts is reported again, and the tach has to be seen working again.

It is configured with `sanityCheck.fanStalled.enabled` per fan (default `true`). Fans with `neverStop` are never
checked here, whatever the config says: their own stall detection (above) raises the PWM and notifies when the fan
stalls at `maxPwm`, so a fan is never reported by both. Fans without an RPM sensor (`cmd`/`file` fans without an
RPM source, NVIDIA fans that can't read the RPM) are never checked.

**Why**: The only stall check used to be the `neverStop` one. A fan without it could read 0 RPM even at `maxPwm`
while the controller reported `running`, without a log line or notification. Typical causes are a broken or blocked
fan, or a configured `minPwm` below the PWM the fan needs to start (with `kickStart.mode: off`, or a kick that
didn't start the fan).

**Who benefits**: Users without `neverStop` who want to know that a fan stopped working.

**Alternatives considered**: Raising the offset like `neverStop` (it would stop the controller at `maxPwm`, which
breaks setups with an RPM input that has no tach connected). A start kick is a separate feature (see below). Only checking the RPM average would report a fan that spins up slowly (e.g. 100 RPM after a start, the
average needs about 7 polls to reach 50 RPM), so the last reading must show a stopped fan too.

**Trade-offs**: A fan that a third party (e.g. the device firmware) stops on purpose while fan2go writes a positive
PWM value is reported; disable the check for it. A tach that worked once and then fails with 0 RPM is reported as a
stall. Like for `neverStop`, the first report takes a while: from 1000 RPM with the defaults, the average needs 29 s
to drop below 50 RPM. A target that alternates at `minPwm` (e.g. PID jitter) keeps restarting the reaction period,
and so does a fan whose reported PWM never matches the expected value while `pwmValueChangedByThirdParty` is on.

### Kick start from standstill

**Decision**: Every start of a fan from standstill is supervised for a short while. A fan that stands still gets a
kick: a higher PWM value until it spins, then its normal target (`kickStart` in `internal/controller/kick_start.go`,
issue #30). `UpdateFanSpeed` calls it after the scaling and the `neverStop` floor, before the `neverStop` stall
detection and the write.

When the supervision starts (all must hold):

- the new target (before the `pwmMap`) should make the fan spin: it is above 0, and the `pwmMap` doesn't map it to 0;
- the last target didn't, or there is none yet in this run of the controller (first update after a start or reload).
  A target that changes while the fan is supposed to spin never starts a supervision, even if the fan stopped: that
  is a target below what the fan needs to keep spinning, and kicking it would make it start and stop over and over.
  The stall checks handle that case. The supervision is armed again only by a target that stops the fan;
- the fan has an RPM sensor that has been seen working: an analyzed fan (hwmon, nvidia) whose RPM curve has a
  spinning point, or any fan that the RPM monitor saw spinning in this run (the curve data of file and cmd fans is
  not measured). For fans without one, see below.

How a start is supervised:

- **Kick right away** if a direct RPM reading and the last 2 samples of the RPM monitor all show standstill (below
  50 RPM, `fans.IsRpmLikelySpinning`) and the kick value is above the target. At the first update of a run, the
  monitor usually has only one sample: the start is watched instead, and a fan that stands still when fan2go starts
  is kicked about 3–6 s later.
- **Otherwise the target is set and watched** (`kickPhaseWatching`): a single reading can be stale (a driver that
  caches the tach value), show a coasting fan, or be garbage (e.g. another program reading the same chip at the same
  time: one reading of all channels was wrong, 57 RPM for a fan at 800 RPM). If 2 samples in a row, taken after the
  fan could react (`responseDelay` after the write), show standstill, the fan is kicked. This also covers a kick
  value that isn't above the target (e.g. a configured `startPwm` that is also `minPwm`): the target is tried first,
  and if it doesn't start the fan, `mode: auto` kicks one step above it.
- **During a kick** (`kickPhaseKicking`), the kick value is held until 2 samples in a row, taken after the readback
  delay, show the fan spinning. A single spinning reading doesn't end it. Then the target is written through the
  normal path (the control algorithm kept running during the kick), and the start is watched again. There is no ramp
  from the kick value down to the target: the fan slows down by its inertia. A kick that starts the fan takes 1–3 s,
  depending on the fan and its tach.
- **A start succeeds** once 2 samples in a row, taken after the coast-down allowance plus the response delay, show
  the fan spinning at its target. A fan that slowly coasts to a stop is still seen spinning before that. Without such
  samples, the supervision ends unjudged after `coastDown + 2 × watch window`.
- **A fan that stops at its target after a kick made it spin** didn't fail to start: the target doesn't keep it
  spinning. It wasn't up to speed yet, or the target is below the PWM value that keeps the fan spinning (e.g. a
  configured `minPwm` that is too low). It gets one more kick in this start, one step higher (`mode: fixed`: at the
  same value). If the fan then keeps spinning, `mode: auto` uses that value for later starts. If it stops again, the
  start ends, and fan2go warns once per run that the target is below what keeps the fan spinning (raise `minPwm`, or
  set `neverStop: true`). Starts at that target or below aren't supervised for the rest of the run (`tooLowTarget`,
  kept across an error restart of the controller, reset by a reload); a higher target, e.g. after `minPwm` was raised
  or the `neverStop` floor rose, is supervised again. Neither counts as a failed kick, raises the kick value by
  itself, or disables the kick start: a too-low target costs at most 2 kicks per run, never an escalation to full
  speed.
- A kick that didn't start the fan counts as failed after `kickTimeout`, and only if 2 samples in a row show the fan
  standing still and reading the RPM doesn't fail. While the samples can't judge the kick (reading the RPM fails, or
  they disagree), it is held, but at most `3 × kickTimeout`: then the fan gets its target unjudged, like a fan
  without an RPM sensor.
- A target that stops the fan ends the supervision; it doesn't count as failed. A curve failure ends it too, the
  `onSensorFailure` action decides the PWM value then.
- A kick is counted and its timer starts only after the write succeeded. A failed write changes nothing.

The RPM monitor stores each new reading as one immutable sample (`rpmSample`: value, time, the number of samples in
a row with the same state, the time of the previous one), so the update loop never mixes the value of one reading
with the time of another. A reading other than 0 that repeats the value of the last sample within 3/4 of the tach
interval (at least 2.5 s, at most 5 s) is not a new sample: a driver that caches the RPM value reports the same
sample (also a wrong one) again, while the value of a spinning fan changes from one tach update to the next. A
reading of 0 is always a new sample, so a stopped fan is seen at once, also with a slow tach. Samples are judged by
the time they were taken, not by their number: a sample taken before the fan could react doesn't count (for a
standstill verdict, the last sample must be taken after `responseDelay`, the one before after `responseDelay -
tachInterval`). Samples taken between two updates count too (an update loop slower than the RPM monitor).

Timing (derived from what the analysis measured, see
[fan-analysis.md](fan-analysis.md#measure-the-breakaway-pwm-value); defaults for data without it):

- `tachInterval` = the measured tach update interval (default 1 s), at least `rpmPollingRate`;
- `responseDelay` = readback delay + spin-up delay (measured, default 2 s) + `tachInterval`;
- watch window = `responseDelay + 2 × tachInterval` (5 s with the defaults);
- `kickTimeout` = max(watch window, 4 s, `kickStart.duration`);
- `coastDown` = the longest coast-down time measured by the analysis (default 5 s, at most 30 s): the allowance
  before spinning samples confirm a start.

Measured values above their limits (10 s; coast-down 30 s) are capped, and logged at INFO.

The kick value (before the `pwmMap`):

- `mode: fixed` uses `kickStart.pwm`.
- `mode: auto` uses, in this order, `kickStart.pwm`, a configured `startPwm` (its documented meaning is "the lowest
  PWM value at which the fan starts from standstill"), the breakaway PWM value measured by the analysis plus a margin
  of 1/32 of the full range (8; nvidia: 4), or, without a measured value (data from an older version, `fan init` not
  run yet), `startPwm + (max - startPwm) / 4`. The first kick value of a run is logged at INFO once the fan is kicked.
- Every kick is at most `fans.FailSafeMaxPwm` (the configured `maxPwm`, else full speed). The target is written
  instead of the kick value if it rises above it. On it87 chips that report `pwm_enable` 0 at PWM 255 (see
  [hardware-quirks.md](hardware-quirks.md)), a kick at full speed is not mistaken for a third party.

When a start fails:

- `mode: auto` raises the kick value by 1/8 of the full range (32; nvidia: 13; at least to the next value with
  another raw value in the `pwmMap`) and kicks again right away, up to `fans.FailSafeMaxPwm`. The raised value is
  kept for the rest of the run, so later starts use it at once. After 5 kicks in a row that started the fan at the
  first attempt, a raised value is lowered by half a step again: never below the first value of the run, and never
  to (or within half a step above) a value that failed in this run.
- If even the kick at `fans.FailSafeMaxPwm` fails, `mode: auto` gives up: it logs a warning and doesn't kick the fan
  again until its config is reloaded or fan2go restarts (this survives the restart of the controller after an error).
  A broken tach would otherwise get a full-speed burst at every start. The fan gets its target, and the stall checks
  take over.
- `mode: fixed` doesn't raise `kickStart.pwm`: it logs a warning and the fan gets its target.

Fans whose RPM can't be checked (no RPM sensor, or one not seen working): fan2go can't see standstill, so it assumes
it at every start from a stopping target and at the first update of a run. With an explicit kick value (`mode: fixed`,
or `mode: auto` with `kickStart.pwm` or a configured `startPwm` above `minPwm`), the fan is kicked at it for
`kickStart.duration` (default 3 s, at least 1 s). Without one, `mode: auto` doesn't kick it and logs that once at
INFO, pointing to `kickStart.pwm` and `mode: fixed`.

Interaction with the other checks:

- While a start is supervised, neither the `neverStop` stall detection (and its `minPwm` raise) nor `checkStall` run.
  When the supervision ends, both give the fan their full reaction period again (`pwmFloorSetAt`,
  `restartStallReactionPeriod`). So a fan with `neverStop` gets kicked first, and its `minPwm` offset is only raised
  if the kicks failed.
- The kick is written through `setTargetPwm` like any target, so `lastTarget` is the kick value and the
  `pwmValueChangedByThirdParty` check compares the readback with it: no false third-party warnings.
- Statistics: `KickStartCount` (every kick, also raised ones), `FailedKickStartCount`, `KickStartPwm` (value of the
  last kick, 0 if none in this run); metrics `fan2go_controller_kick_start_count`,
  `fan2go_controller_failed_kick_start_count`, `fan2go_controller_kick_start_pwm`. Kicks are logged at DEBUG, the
  first kick value of a run and a raised or lowered value at INFO, giving up as a WARNING.

**Why**: Fans have static friction: the PWM value that starts a stopped fan (breakaway) is often much higher than the
one that keeps it spinning, and the analysis measures the start boundary while it moves the PWM value up and down, so
it is closer to the latter. A large, slow case fan that spins down to PWM 32 needed PWM 116 to start. fan2go set it
to its `startPwm` 45 from standstill, and it stayed at 0 RPM. Without `neverStop`, it stayed stopped (the stall check
reported it). With `neverStop`, the stall detection raised `minPwm` by 1 every 3 s until PWM 116, logged every step,
and kept the higher floor for the run.

Tach readings are slow and sometimes wrong: some drivers update the RPM value only every second or two, a fan takes
a while to spin up (BLDC fans with locked-rotor protection retry only every few seconds), and a chip read by two
programs at once can return a garbage value. So no decision is made on a single reading, and the timing comes from
the measured fan instead of fixed values.

**Who benefits**: Users of large or slow fans, fans with a zero-RPM mode (curves that stop the fan), and `neverStop`
users whose fan stands still when fan2go takes over.

**Alternatives considered**:

- Always kick at full speed (like some BIOS fan controls): reliable, but an audible burst at every start.
- Use the derived `startPwm` as the kick value: that is the value that failed above.
- Decide on the first reading after the write: a stale reading of a spinning fan skips a needed kick, a garbage low
  reading kicks a spinning fan, a garbage high reading ends a kick too early. Two readings in a row cost about one
  tach interval more.
- A fixed kick duration with an RPM sensor: either too short for slow fans or longer than needed. Holding until the
  RPM shows the fan spinning is the shortest kick that works.
- Kick whenever a fan with a spinning target reads 0 RPM: kicks a fan whose target is below its keep-spinning PWM
  over and over (kick, start, slow down, stop, kick). Only starts from a stopping target or from the first update
  are supervised.
- Persist the value raised at runtime: the analysis data only changes on an explicit analysis so far (see
  [fan-analysis.md](fan-analysis.md#no-continuous-re-analysis-at-runtime)); a raised value is learned again quickly.
- Kick fans without an RPM sensor by default (`mode: auto`): fan2go could see neither standstill nor whether the kick
  worked, so every start would be a blind burst at a guessed value. An explicit value is required for them.

**Trade-offs**: A kick ends after two RPM readings that show the fan spinning, so the fan is briefly faster than its
target (1–3 s at the kick value). A kick that fails costs `kickTimeout` (4–5 s with the defaults) per step: from a
measured value that is too low, the fan starts a few steps later. A start whose first reading isn't clearly standstill
is watched first, so a fan that really stands still is kicked one response delay later (about 3–6 s). Every start is
supervised for about `coastDown + responseDelay + 2 × tachInterval` (about 10 s with the defaults), at most
`coastDown + 2 × watch window` (15 s with the defaults) if the readings don't confirm it; the stall checks wait
until then. Without a
measured value, the first kick is a guess: it may be louder than needed, or fail and be raised. The RPM is read
directly once per possible start (a command for cmd fans). With `mode: auto`, the analysis takes longer (see
[fan-analysis.md](fan-analysis.md#measure-the-breakaway-pwm-value)).

### RPM monitoring with an exponential moving average

**Decision**: The RPM monitor goroutine (only for fans with an RPM sensor) reads the RPM every `rpmPollingRate` and
updates `fan.SetRpmAvg` with an exponential moving average, α = 1/`rpmRollingWindowSize`. The first successful
reading seeds the average (`rpmAvgSeeded`), a failed reading leaves it unchanged, and once the average is within 0.5
of the reading, it takes the reading's value exactly (`util.UpdateExponentialMovingAvg`). `GetRpmAvg` returns
false until the average is seeded.

**Why**: Raw tach readings are noisy. Seeding avoids an average that starts at 0 and causes a spurious stall at
startup. Snapping to the input fixes averages that stayed just below a constant input forever (#547).

**Who benefits**: `neverStop` users; users of the RPM values in the statistics and REST API.

Like the sensor monitor ([sensors.md](sensors.md)), it logs only the first failed reading of a series and the
recovery (with the number of failed readings), not every poll (`rpmReadFailures`).

**Trade-offs**: `rpmRollingWindowSize` is not a window, despite its name. A tach that fails for a long time is only
visible in the first log line; there is no metric for failed RPM reads yet.

### Controller state

**Decision**: Each controller keeps its state (`controller/status.State`), the time it entered it and the error that
caused it, under `statsMutex`. `GetStatus` returns a snapshot. The Prometheus metric `fan2go_controller_state{id,state}`
and the REST API (`/controller`, `/controller/<fan id>`) show it.

| State | Meaning |
|---|---|
| `stopped` | Not started yet, or stopped cleanly (shutdown, reload, cancelled during startup) |
| `initializing` | `Run` started: sensor warm-up, fan initialization, `setPwm -> getPwm` detection, until the first update |
| `running` | Normal control (set by the first update that could evaluate the curve) |
| `sensor_failure_max` | The curve fails, the fan is held at `maxPwm` (`onSensorFailure: max`, or `restore` that couldn't hand the fan over) |
| `sensor_failure_restored` | The curve fails, the fan was handed back (`onSensorFailure: restore`) |
| `stopped_pwm_write_failed` | Stopped after `ErrPwmWriteFailed` |
| `stopped_stalled` | Stopped after `ErrFanStalledAtMaxPwm` |
| `stopped_error` | `Run` returned another error (e.g. initialization, manual control, panic) |

The update loop records the reason when it stops the controller (`stopAfterUpdateError`); `Run` doesn't overwrite
it when it returns. A `Run` that starts again (after a failed reload) starts with `initializing`. While the curve
fails, every update sets the `sensor_failure_*` state again. `running` is only set by an update that could evaluate
the curve, so a controller whose curve still fails when `Run` starts again re-enters the failure state on its first
update, without reporting `running` in between: alerts on the failure state don't resolve and fire again. If the
curve recovers but manual control can't be taken again, the state stays `sensor_failure_restored` with that error.

**Why**: Before, a fan in a failure state or a stopped controller was only visible in the log and a desktop
notification. Headless users couldn't alert on it.

**Who benefits**: Users who monitor fan2go with Prometheus, and UI clients of the REST API.

**Trade-offs**: The state is per run of the controller; a reload creates new controllers that start at `stopped`.
The metric has one series per state and controller (8 per fan).

### Locking: leaf locks in the fans, snapshots for JSON

**Decision**: Each fan has a `sync.RWMutex` that protects all mutable fields. It is a leaf lock: no sysfs, NVML or
command I/O and no call to another fan method happens while it is held. Paths are copied under the lock, the I/O
runs unlocked, results are stored under the lock. `GetFanRpmCurveData` returns a copy. `MarshalJSON` serializes a
snapshot taken under the lock. In the controller, `statsMutex` protects the statistics and `lastSetPwm`,
`curveMutex` protects the curve reference, and `rpmAvgSeeded` is an `atomic.Bool`. Everything else (`lastTarget`,
`minPwmOffset`, failure states) is only used by the update loop.

**Why**: The RPM monitor, the update loop, the statistics collector and the REST API access the same fan from
different goroutines. Several of these accesses were unsynchronized, and copying a `FanConfig` while it changes can
produce torn values (#544). A leaf lock can't deadlock, and slow I/O (a 2 s `cmd` command) never blocks readers.

**Who benefits**: REST API and Prometheus users; maintainers (`make test` runs with `-race`).

**Trade-offs**: A fan's fields can change between two getter calls, so callers must not assume a consistent view
across calls. The "update loop only" rule for most controller fields is a convention documented in comments, not
enforced.

### Reload rebuilds everything

**Decision**: On SIGHUP or a changed config file, the daemon first reads and validates the new config while the
controllers keep running; an invalid config leaves them untouched. While a controller analyzes its fan
(`IsInitializing`), the reload is deferred. Then the daemon stops all controllers (each restores its fan) and the
curve evaluator, and starts new fans, a new evaluator and new controllers (`runOrchestrator`, `applyConfiguration`).
If the new objects can't be created, the old controllers and evaluator are started again with the old config. `Run`
resets the runtime state of a controller at its start (`resetRuntimeState`: failure states, last PWM, `minPwmOffset`,
stall timers, RPM average seed, the control algorithm via `ControlLoop.Reset`), so a restarted controller controls
the fan like a new one. It keeps the fan state from before fan2go first took over the fan, whether the fan still
has to be handed back (`handbackPending`) and what it knows about the saved original state in the database
(`originalFanStateSaved`, `originalFanStateUnusable`), the cumulative statistics counters and the log-once flags.
If its last restore was incomplete, that counts as a write (`mayHaveWrittenToFan` starts as `handbackPending`), so
`Run` restores the fan also on early errors. A failed save of the original state is tried again in the next `Run`.

**Why**: New objects don't carry stale state (offsets, overrides, failure states) into a changed config, and the
reload reuses the tested shutdown path. **Who benefits**: users who tune their config while fan2go runs.

**Trade-offs**: During a reload, each fan is handed back for about 3.2 s (warm-up plus update delay) before the
new controller takes over. The `minPwmOffset` and runtime sanity check overrides are lost. The new controller takes
its snapshot after the old one restored the fan; if that restore was incomplete, the old controller kept the saved
original state in the database, and the new controller uses it (without a working database, the new snapshot is
fan2go's own state).

## Configuration

| Key | Default | Effect |
|-----|---------|--------|
| `fanController.adjustmentTickRate` | `200ms` | Curve evaluator cycle, so the fan update rate. Must be > 0. |
| `controllerAdjustmentTickRate` | unset | Deprecated alias, copied to `fanController.adjustmentTickRate` with a warning. |
| `fanController.pwmSetDelay` | `5ms` | Minimum wait between set and read-back during PWM map detection and the analysis; the measured readback delay is used if longer ([fan-analysis.md](fan-analysis.md)). |
| `rpmPollingRate` | `1s` | RPM monitor rate; also the unit of the stall reaction period (3 periods). |
| `rpmRollingWindowSize` | `10` | n of the RPM moving average (α = 1/n). |
| `tempSensorPollingRate` | `200ms` | Added to the 2 s startup warm-up; also sets the default `sensorStaleTimeout`. |
| `sensorStaleTimeout` | max(10s, 10 x `tempSensorPollingRate`) | When a sensor counts as stale, so when `onSensorFailure` applies. See [sensors.md](sensors.md). |
| `fans[].neverStop` | `false` | Floor at `minPwm`; with an RPM sensor, stall detection. |
| `fans[].minPwm`, `startPwm`, `maxPwm` | from analysis | Range for scaling; validation requires `minPwm`, `startPwm` <= `maxPwm` if set; derived values are clamped. |
| `fans[].useUnscaledCurveValues` | `false` | Skip the scaling to `[minPwm..maxPwm]`. |
| `fans[].pwmMap`, `setPwmToGetPwmMap` | autodetect | See [fan-analysis.md](fan-analysis.md). |
| `fans[].controlMode.active` | `pwm` | Mode written when fan2go takes control: `pwm`/`manual`, `disabled`, `auto`, or an integer. |
| `fans[].controlMode.onExit` | `restore` | `restore`, `none`, or `mode` and/or `speed` (0..255, through the `pwmMap`). |
| `fans[].onSensorFailure` | `max` | `max` or `restore`. |
| `fans[].sanityCheck.pwmValueChangedByThirdParty.enabled` | `true` | Compare the reported PWM with the last target. |
| `fans[].sanityCheck.fanModeChangedByThirdParty.enabled` | `true` | Re-apply `controlMode.active` if the mode changed. |
| `fans[].sanityCheck.fanModeChangedByThirdParty.throttleDuration` | `10s` | Minimum time between two mode checks. |
| `fans[].sanityCheck.fanStalled.enabled` | `true` | Warn and notify once when a fan without `neverStop` doesn't spin at a target of at least `minPwm`. |
| `fans[].kickStart.mode` | `auto` | `auto`, `fixed` or `off`, see [Kick start from standstill](#kick-start-from-standstill). |
| `fans[].kickStart.pwm` | none | Kick PWM value (before the `pwmMap`), 1..255, at most a configured `maxPwm`. Required for `mode: fixed`; with `mode: auto` the first kick value (raised if it fails), and it lets `auto` kick a fan without a working RPM sensor. |
| `fans[].kickStart.duration` | `0` (derived) | 0..60 s, a plain number is seconds. With a working RPM sensor: the time after which a kick counts as failed, at least the derived `kickTimeout`. Without one: the time a kick is held (default 3 s, at least 1 s). |
| `fans[].cmd.setPwm/getPwm/getRpm.timeout` | `2s` | Command timeout of `cmd` fans. |

Not configurable (constants in `internal/controller/controller.go` and `kick_start.go`): startup warm-up 2 s, update
delay 1 s, stall threshold 50 RPM, stall reaction 3 RPM polling periods, PWM write failure timeout 10 s and 3 attempts,
log interval 25 for repeated failures; kick start margin 1/32, step 1/8 of the full range, fallback a quarter of the
range above `startPwm`, step down after 5 kicks in a row that worked at once, 2 readings in a row for every verdict,
`kickTimeout` at least 4 s, default tach interval 1 s and spin-up delay 2 s (measured values capped at 10 s), default
coast-down 5 s (capped at 30 s), a kick that can't be judged ends after 3 kick timeouts, one extra kick after a stop
at the target.

## Known limitations and open questions

- Without config values `minPwm` equals the measured `startPwm` (the start boundary of the analysis, closer to the
  PWM that keeps the fan spinning than to the one that starts it); the PWM that starts the fan is only used for the kick
  start. With `kickStart.mode: off`, a configured `minPwm` below `startPwm` without `neverStop` can leave a fan
  stopped; config validation warns about it, and the stall check reports it at runtime.
- Kick start: the kick PWM value raised at runtime is not persisted, a restart or reload starts from the measured
  value again. A fan that is stopped by something else while fan2go sets a spinning target (e.g. firmware, a third
  party) is not kicked: only a start from a stopping target or the first update of a run is. A fan without a
  working RPM sensor is only kicked with an explicit value, blindly at every such start (including every start or
  reload of fan2go). A broken tach that reads 0 RPM makes `mode: auto` kick once up to full speed per run, then it
  stops kicking.
- The `fanModeChangedByThirdParty` check expects PWM mode even with another `controlMode.active`.
- A stopped controller (`ErrPwmWriteFailed`, `ErrFanStalledAtMaxPwm`, panic) is not restarted, and the daemon keeps
  running even if no fan is controlled anymore.
- Also see the trade-offs above: `minPwmOffset` is not persisted, stall detection skips alternating targets and
  steps before the `pwmMap`, a fan left behind by SIGKILL is only restored on the next exit, one global update
  rate, a ~3.2 s handback on every reload.
