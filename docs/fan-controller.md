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
    update loop goroutine (startupUpdateDelay 1s, then per cycle)     handlePwmWriteResult
  restoreControlModeOnce on every exit path
```

Main types and state:

- `FanController` (interface, used by the daemon, statistics and CLI), implemented by `DefaultFanController`.
- `FanStateSnapshot` holds the control mode and raw PWM value the fan had before fan2go touched it, and for hwmon
  fans the driver value behind "automatic" (e.g. `pwm_enable=5`). All are pointers; `nil` means "unknown". While
  fan2go controls the fan, the snapshot is also saved in the database (see below).
- `lastTarget` is the last target fan2go wrote, before the `pwmMap`. `lastSetPwm` is the raw value after the
  `pwmMap`, exposed to statistics through `GetLastSetPwm`.
- `minPwmOffset`, `pwmFloorSetAt`, `rpmAvgSeeded` (stall detection); `sensorFailureAction`, `pwmWriteFailures`,
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
5. `setTargetPwm` maps the target through the `pwmMap` (`applyPwmMapToTarget`) and writes the raw value.

`minPwm`, `startPwm` and `maxPwm` come from the config if set, otherwise from the saved fan analysis
(`AttachFanRpmCurveData`). Without config values, `minPwm` is set to the measured `startPwm`. `startPwm` itself
is not used by the update loop: it is only logged, and `Run` warns if `minPwm > startPwm`. If a derived `minPwm` or
`startPwm` ends up above `maxPwm` (e.g. `maxPwm: 100` and a measured start of 120), `AttachFanRpmCurveData` logs a
warning and clamps (`clampDerivedPwmBoundaries`), so the scaling can't run backwards. A configured value always
wins: with a configured `maxPwm`, the derived `minPwm`/`startPwm` are lowered to it; otherwise the derived `maxPwm`
is raised to the (configured or derived) `minPwm`/`startPwm` (a higher PWM is the safe direction), as far as the
fan allows (nvidia: 100); derived values still above it are lowered. fan2go has no
"jump-start" from standstill (open issue #30). File and cmd fans always report `minPwm` 0 and `maxPwm` 255; nvidia
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
config under the fan lock. The REST API shows it as `"Value": false, "RuntimeOverride": true`.

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
accepted without verification.

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
saved state is still its true original. Running `fan2go fan init` while the daemon controls the
same fan uses and then deletes the daemon's entry; that combination is not supported anyway.

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

- `max` (default): write `maxPwm` (through the `pwmMap`) on every cycle while the failure lasts. A failed write is
  logged once per failure state, and it does not count towards the PWM write timeout.
- `restore`: hand the fan back once, with the same logic as on exit. It only counts as success if the fan ends up
  in a control mode other than manual PWM (`restoreControlMode` returns `handedOver`). Otherwise (`onExit: none`,
  only `onExit.speed`, original mode manual, mode unknown or not writable), the fan is set to `maxPwm` like `max`.
- Recovery (`recoverFromCurveFailure`): after `restore`, take manual control again (like at startup) and arm a new
  `restoreOnce`. If that fails, the fan stays handed back and fan2go tries again on the next cycle.
  Before that, `calculateTargetSpeed` makes the control loop continue from the fan's speed: `Preset` at the curve
  value of `maxPwm` after `max`, `Reset` (start at the target) after `restore`. So the fan comes down from max
  (with the time constant or ramps of the control algorithm) instead of jumping (see [control-algorithms.md](control-algorithms.md)).
- Entering the failure state is logged and notified once, after the fan is in a safe state. Recovery is logged
  once. The stall detection is paused while the curve fails, because `UpdateFanSpeed` returns before it.

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
`hadTimeToReactToPwmFloor`). If the fan is still stalled at `maxPwm`, `UpdateFanSpeed` returns
`ErrFanStalledAtMaxPwm`, and the controller restores the fan and stops. The offset is in the statistics
(`MinPwmOffset`, `IncreasedMinPwmCount`), but it is not persisted. Without an RPM sensor, `neverStop` only sets
the PWM floor, and fan2go logs a warning at startup.

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
| `fanController.pwmSetDelay` | `5ms` | Wait between set and read-back during PWM map detection ([fan-analysis.md](fan-analysis.md)). |
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
| `fans[].cmd.setPwm/getPwm/getRpm.timeout` | `2s` | Command timeout of `cmd` fans. |

Not configurable (constants in `internal/controller/controller.go`): startup warm-up 2 s, update delay 1 s, stall
threshold 50 RPM, stall reaction 3 RPM polling periods, PWM write failure timeout 10 s and 3 attempts, log interval
25 for repeated failures.

## Known limitations and open questions

- `startPwm` is not used at runtime; without config values `minPwm` equals the measured `startPwm`, and there is no
  jump-start from standstill (#30).
- The `fanModeChangedByThirdParty` check expects PWM mode even with another `controlMode.active`.
- A stopped controller (`ErrPwmWriteFailed`, `ErrFanStalledAtMaxPwm`, panic) is not restarted, and the daemon keeps
  running even if no fan is controlled anymore.
- Also see the trade-offs above: `minPwmOffset` is not persisted, stall detection skips alternating targets and
  steps before the `pwmMap`, a fan left behind by SIGKILL is only restored on the next exit, one global update
  rate, a ~3.2 s handback on every reload.
