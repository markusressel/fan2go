# Fan controller (runtime)

fan2go runs one fan controller per configured fan. It takes the value of the fan's curve, runs it through the
fan's control algorithm, maps the result to a PWM value the hardware accepts, writes it, and watches the fan for
stalls, foreign changes and failures. When it stops, it hands the fan back. The code lives in
`internal/controller/controller.go` (`DefaultFanController`) and the fan backends in `internal/fans`
(`HwMonFan`, `NvidiaFan`, `FileFan`, `CmdFan`). The daemon wiring is in `internal/daemon.go` and `internal/factory.go`.

This document covers the controller after the fan is set up. The initial fan analysis, the detection of the
`pwmMap` and `setPwmToGetPwmMap` and their persistence are described in [fan-analysis.md](fan-analysis.md).
The control algorithms (`pid`, `direct`) are described in [control-algorithms.md](control-algorithms.md), sensor
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
  takeManualControl (controlMode.active)                              controlLoop.Cycle(value)
  run.Group:                                                          clamp, scale to [minPwm..maxPwm]
    RPM monitor goroutine (every rpmPollingRate)                      neverStop floor + stall detection
      measureRpm -> exponential moving average                        setTargetPwm -> pwmMap -> fan.SetPwm
    update loop goroutine (startupUpdateDelay 1s, then per cycle)     handlePwmWriteResult
  restoreControlModeOnce on every exit path
```

Main types and state:

- `FanController` (interface, used by the daemon, statistics and CLI), implemented by `DefaultFanController`.
- `FanStateSnapshot` holds the control mode and raw PWM value the fan had before fan2go touched it. Both are
  pointers; `nil` means "unknown".
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
is not used by the update loop: it is only logged, and `Run` warns if `minPwm > startPwm`. fan2go has no
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

### Control modes: take manual control, fall back to `disabled`

**Decision**: `takeManualControl` writes `controlMode.active` (default `pwm`) through `fan.SetControlMode`. If
writing `pwm` fails, `trySetManualPwm` tries `disabled` (`pwm_enable=0`, full speed on hwmon) instead. Any other
`active` mode has no fallback. If the write fails but the fan reads back as manual PWM anyway, the error is ignored.
If none of this works, `Run` returns the error and does not control the fan. Fans that cannot write a control mode
(file, cmd, hwmon without `pwm_enable`) skip this step.

On hwmon, `SetControlMode` reads `pwm_enable` back to verify it. Any value `>= 2` counts as automatic; the last
automatic value seen (e.g. 5 on nct6775) is remembered and written when fan2go hands the fan back to automatic.
Integer `active` values `>= 3` are written as is. A write-only `pwm_enable` (e.g. `dell_smm_hwmon`, #229) is
accepted without verification.

**Why**: fan2go cannot set the speed unless the driver is in manual mode. The `disabled` fallback dates back to
2022; the reason is not documented. It likely aims at drivers that accept `pwm_enable=0` but not 1, so that the fan
at least runs at full speed instead of being left in an unknown state.

**Who benefits**: nct6775-style drivers with several automatic modes; write-only (#229) or missing (#116) `pwm_enable`.

**Trade-offs**: If the fallback to `disabled` succeeds, `takeManualControl` returns no error and the controller
keeps running, although on most drivers PWM writes have no effect in that mode. The fan then runs at full speed
while fan2go believes it controls it.

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

**Trade-offs**: SIGKILL, OOM kills and power loss still leave the fan at its last PWM. Ctrl-C twice still waits
for the restore. A stopped controller stays stopped until restart or reload.

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
is constant, so a target that alternates between two values (e.g. PID jitter) mostly skips it (#547). The step is
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

**Trade-offs**: `rpmRollingWindowSize` is not a window, despite its name. RPM read errors are logged on every poll,
unlike temperature sensors, whose monitor logs only the first failure and the recovery ([sensors.md](sensors.md)).

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

**Decision**: On SIGHUP or a changed config file, the daemon stops all controllers (each restores its fan) and the
curve evaluator, then reads and validates the new config, and starts new fans, a new evaluator and new controllers
(`runOrchestrator`, `reloadConfiguration`). If the new config is invalid, the old controllers and evaluator are
started again with the old config.

**Why**: New objects don't carry stale state (offsets, overrides, failure states) into a changed config, and the
reload reuses the tested shutdown path. **Who benefits**: users who tune their config while fan2go runs.

**Trade-offs**: During a reload, each fan is handed back for about 3.2 s (warm-up plus update delay) before the
new controller takes over. The `minPwmOffset` and runtime sanity check overrides are lost. The new controller takes
its snapshot after the old one restored the fan; if that restore was incomplete, the new snapshot is fan2go's own
state, not the original one.

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
| `fans[].minPwm`, `startPwm`, `maxPwm` | from analysis | Range for scaling; validation requires `minPwm`, `startPwm` <= `maxPwm` if set. |
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
- If only `maxPwm` is configured and it is below the measured `startPwm`, `minPwm > maxPwm` is possible. Validation
  only checks configured values, so the scaling then runs backwards.
- A successful fallback to `disabled` hides a failed switch to `pwm` mode; the `fanModeChangedByThirdParty` check
  expects PWM mode even with another `controlMode.active`.
- A stopped controller (`ErrPwmWriteFailed`, `ErrFanStalledAtMaxPwm`, panic) is not restarted, and the daemon keeps
  running even if no fan is controlled anymore.
- After a failed reload, the old controller objects run again with the state of their previous run (failure
  states, `minPwmOffset`, stall timers, RPM average). Only `restoreOnce` and `mayHaveWrittenToFan` are reset.
- Also see the trade-offs above: `minPwmOffset` is not persisted, stall detection skips alternating targets and
  steps before the `pwmMap`, no restore after SIGKILL, one global update rate, a ~3.2 s handback on every reload.
