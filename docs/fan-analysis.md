# Fan initialization and analysis

Before fan2go can control a fan well, it needs to know how the fan reacts to PWM values: which values the driver
accepts, what it reports back, at which PWM the fan starts to spin and how the RPM grows with the PWM value. fan2go
measures this once per fan (the "initialization sequence") and stores the result in a local bbolt database, so later
starts skip the measurement. The code lives in `internal/controller/fan_analysis.go` (the RPM curve measurement),
`internal/controller/controller.go` (when to analyze, the PWM mappings, locking and hand-back),
`internal/persistence/persistence.go` (storage), `internal/fans/common.go` (deriving `startPwm`/`maxPwm`) and
`cmd/fan/init.go` / `cmd/fan/reset.go` (CLI).

## How it works

A fan controller (`DefaultFanController.Run`) goes through these steps before it starts its control loop:

```
Run
 ├─ prepareController            create the db directory, warn if neverStop can't be checked
 ├─ storeInitialFanState         snapshot pwm_enable and PWM for the hand-back
 ├─ wait sensor warm-up          2s + tempSensorPollingRate
 ├─ runInitializationIfNeeded
 │    LoadFanRpmData ── ok ─────────────────────────────────────────────┐
 │        ├─ missing / corrupt (hwmon, nvidia) ─► RunInitialization      │
 │        │      lock (if runFanInitializationInParallel: false)         │
 │        │      computeFanSpecificMappings                              │
 │        │         setPwmToGetPwmMap: config | db | sweep 0..255        │
 │        │         pwmMap:            config | db | derive from sweep   │
 │        │      FanCurveAnalyzer.RunInitializationSequence              │
 │        │         start boundary ─► max boundary ─► interior ─► cleanup│
 │        │      AttachFanRpmCurveData, SaveFanRpmData                   │
 │        ├─ missing (file, cmd fan) ─► save the fixed linear curve      │
 │        └─ other error (e.g. db locked) ─► stop with an error          │
 ├─ LoadFanRpmData + AttachFanRpmCurveData  ◄───────────────────────────┘
 │      derives startPwm, maxPwm (and minPwm = startPwm) unless configured
 ├─ computeFanSpecificMappings   (loads the saved mappings; sweeps again if
 │                                 setPwmToGetPwmMap is missing or corrupt)
 └─ takeManualControl, control loop  (see fan-controller.md)
```

Main types and data:

- `setPwmToGetPwmMap` (`map[int]int`): for each raw value written to `pwm`, the value the driver reports back. Used to
  verify writes during the analysis and for the "third party changed the PWM" sanity check.
- `pwmMapping` (`[256]int`, configured as `pwmMap`): maps fan2go's internal target range 0..255 to the raw values that
  are written to the fan.
- `targetValuesWithDistinctPWMValue`: the target values (pre-`pwmMap`) whose mapped raw values differ. The analysis
  only measures these, so a fan with four raw speeds is measured at four target values, not 256.
- The RPM curve (`map[int]float64`): target value 0..255 to RPM. After cleanup it has a key for every value 0..255.
- `FanCurveAnalyzer`: runs the measurement. It writes through the controller (`setTargetPwm`), so the `pwmMap` is
  applied exactly once (#541).

### The RPM curve measurement

`RunInitializationSequence` skips the measurement for fans without an RPM sensor and returns no curve data. Otherwise
it switches the fan to manual mode and runs four phases over the distinct target values:

1. **Start boundary** (`detectStartBoundary`): binary search for the lowest target value at which the fan spins.
   "Spinning" means at least 50 RPM (`rpmNoiseThreshold`, `fans.IsRpmLikelySpinning`). Each step waits for the fan to
   settle, up to `analysis.settleTimeout`. Every non-spinning probe is stored as 0 RPM. If no value spins,
   the analysis fails with "unable to detect start boundary, no spinning PWM found".
2. **Max boundary** (`detectMaxBoundary`, `confirmMaxBoundary`): a fast scan of the top distinct values (the top 65,
   or from the start boundary if that is higher) in steps of `analysis.coarseStep`, each with a plain
   `fanResponseDelay` wait. It then walks down from the top, measuring every value not yet measured, until one reaches
   95% of the peak RPM. It re-measures the top four values and the neighbours of that candidate with settle waits,
   and picks the highest value that reaches 95% of the confirmed peak.
3. **Interior** (`sampleInteriorCoarse`): measures every `max(2 * analysis.coarseStep, 8)`-th distinct value between
   the two boundaries, with settle waits for jumps of 8 or more target values (in practice always, since the step is
   at least 8 distinct values).
4. **Cleanup** (`rpmCurveMeasurementCleanup`): sets every sample below the noise threshold, and everything below the
   first spinning sample, to 0, adds anchors at PWM 0 and 255, fills the range below the start PWM with step
   interpolation and the rest with linear interpolation, makes the curve monotonically increasing, and smooths the range
   between the derived start PWM and 255 (both exclusive) with a Kalman filter. The start PWM and PWM 255 keep their
   values.

Each point is measured by `measureAtPwm`:

- Write the target value, wait `pwmSetDelay`, read the PWM back and compare it with the expected value from the
  `setPwmToGetPwmMap`. On a mismatch, wait `fanResponseDelay` and try again, up to 3 attempts in total. If it still
  doesn't match, the point is skipped (not an error).
- Wait: `waitForFanToSettle` (if a settle timeout is given) or a plain `fanResponseDelay` sleep.
- Take `analysis.sampleCount` RPM samples, `analysis.sampleDelay` apart, and use their median.

`waitForFanToSettle` reads the RPM every 500 ms, runs it through a Kalman filter and keeps the last 8 values. After 6
values it checks drift of the mean, change of the noise range and the trend. It needs 3 stable checks in a row. If
`analysis.settleTimeout` runs out first, it logs a warning and measures anyway. So a settled point takes at least about
4 seconds.

### How the result is used

`AttachFanRpmCurveData` derives the boundaries from the stored curve with `ComputePwmBoundariesFromCurveData`:

- `startPwm`: the lowest target value with at least 50 RPM.
- `maxPwm`: the highest target value with at least 95% of the peak RPM.
- `minPwm`: set to `startPwm` (there is no separate measurement yet, see #30).

Values configured in the fan config (`minPwm`, `startPwm`, `maxPwm`) always win. The control loop maps curve values
1..255 to `minPwm..maxPwm` (see [Fan controller](fan-controller.md)). The curve itself is also
shown by `fan2go fan --id <id> curve` and returned by the API.

## Design decisions

### Analyze once, persist, and reuse

**Decision**: The analysis runs only when there is no usable saved curve for the fan. The result is saved under the
fan `id` and loaded on every later start and config reload (see [Daemon](daemon.md)).

**Why**: The analysis moves the fan through its whole range, stops it, and takes about one to a few minutes per fan.
Doing that on every boot or reload would be loud and slow (#132 was about slow startup).

**Who benefits**: Everyone after the first start. Headless systems boot to normal fan control within seconds.

**Alternatives considered**: Measuring on every start (rejected for the reasons above). A config-only setup is
possible: with `pwmMap` and `setPwmToGetPwmMap` set to non-autodetect modes and `minPwm`/`startPwm`/`maxPwm` set, the
mappings and boundaries come from the config, but the RPM curve is still measured once.

**Trade-offs**: The data is keyed by the fan `id` only. It goes stale if the hardware behind an `id` changes (other
hwmon index, new fan) or if the `pwmMap` config changes. The user has to run `fan2go fan --id <id> init` (or `reset`)
then.

### Re-analyze only on missing or corrupt data

**Decision**: `runInitializationIfNeeded` starts the analysis only if `persistence.IsMissingOrCorrupt(err)` is true.
Any other error (e.g. the database can't be opened) stops the fan's controller with an error. The same rule applies to
the saved `setPwmToGetPwmMap` and `pwmMap`: a read error doesn't trigger the detection sweep (#549).

Corrupt entries (unparsable JSON, a `pwmMap` without 256 entries) return `persistence.ErrCorruptData`. The `Load*`
functions delete the entry in a separate write transaction, so the fan is analyzed again exactly once and the new
result overwrites it.

**Why**: Before #549, any load error started a new analysis. A database locked by another process for more than a
minute made the daemon sweep the fan, and then saving the result failed anyway. Corrupt entries were "deleted" inside
a read-only transaction, which always failed, and then reported as success with no data. The fan then ran forever
without curve data (`minPwm=0`, `startPwm=255`, `maxPwm=255`).

**Who benefits**: Users with a second fan2go process or a backup tool holding the database: no surprise sweeps. Users
with a damaged database: the fan is analyzed again instead of running with wrong boundaries.

**Trade-offs**: A database error now means "no control" for that fan. If fan2go already wrote to the fan, it hands the
fan back (see the next decisions); otherwise it leaves it alone. The user has to fix the cause and
restart or reload fan2go.

### Two maps for PWM values: `setPwmToGetPwmMap` and `pwmMap`

**Decision**: fan2go keeps two separate mappings (#372, #371). `setPwmToGetPwmMap` describes the readback of the
driver, `pwmMap` describes which raw values are written for a target value. Both can be configured
(`autodetect`, `identity`, `linear`, `values`; #446, #447). Configured modes other than `autodetect` bypass the
database.

In `autodetect` mode:

- `setPwmToGetPwmMap`: `computeSetPwmToGetPwmMapAutomatically` writes every value 0..255, waits `pwmSetDelay` and
  reads `pwm` back. Values whose write fails are left out. Without a readable `pwm` (or with
  `fan init --assume-pwm-map-identity`), it assumes a 1:1 map.
- `pwmMap`: `computePwmMapAutomatically` takes the keys of the `setPwmToGetPwmMap`, i.e. the raw values the driver
  accepted, and spreads them evenly over 0..255 as steps (`util.ExpandMapToFullRange`). If the driver accepts all
  values, this is the identity. If it only accepts e.g. 0..100, the target range 0..255 is quantized onto those values.

**Why**: Some drivers don't report the value that was written (e.g. they report 0..100 or round), others only accept
part of the range. Mixing both into one map broke the third-party check (#371). Without the readback map, every
analysis write would look like a mismatch.

**Who benefits**: Users of unusual hardware (#213, #511). Users whose hardware is known can skip the sweep with
`identity` or set exact values.

**Trade-offs**: The sweep briefly writes every value from 0 to 255 in about 1.3 seconds (256 × 5 ms), including 0. The
derived `pwmMap` ignores the readback values (a `TODO` in the code): a driver that accepts all values but only has a
few real speed steps gets an identity `pwmMap`, so the analysis measures many target values that end up at the same
speed.

### Adaptive boundary search instead of a full sweep

**Decision**: The RPM curve is measured with a binary search for the start boundary, a top-band scan for the max
boundary and coarse interior sampling, followed by interpolation and smoothing (#480, which addressed #104 and #28).

**Why**: The previous approach stepped through the range and took about 7 minutes per fan, and single bad readings
produced spiky curves (#104). The adaptive approach takes about 1.5 minutes and gives a smooth, monotonic curve.

**Who benefits**: Users with several fans and slow fans (less waiting, less noise). The controller, because a
monotonic, full-range curve has no gaps or spikes.

**Alternatives considered**: A full linear sweep with longer waits (#28): accurate, but slow.

**Trade-offs**:

- The binary search assumes "spins at PWM x" is monotonic and has no hysteresis. In practice a fan that slows down
  keeps spinning at lower PWM values than it needs to start from standstill. The search moves up and down, so the
  measured start boundary is a mix of both. fan2go therefore uses the same value for `minPwm` and `startPwm`; #30 is
  still open.
- Only the measured points are real data; the rest is interpolated and smoothed.

### Noise-aware, robust per-point measurement

**Decision**: A point is "spinning" only at 50 RPM or more. Each point is the median of `analysis.sampleCount`
samples. Large jumps wait for the fan to settle, judged adaptively against the observed noise (with
`maxRpmDiffForSettledFan` as the lower bound for the allowed drift), and give up after `analysis.settleTimeout`.

**Why**: Tach signals report small non-zero values for stopped fans and occasional outliers (#104). A fixed wait is
either too short for slow fans or wastes time for fast ones.

**Who benefits**: Users with noisy tach signals and slow-ramping fans or controllers.

**Trade-offs**: The 50 RPM threshold is hard-coded. Fans that really spin below 50 RPM are treated as stopped. The
500 ms sample interval and the settle rules are not configurable.

### Verify the PWM readback and wait `fanResponseDelay` between retries

**Decision**: Before measuring, `measureAtPwm` checks that the fan reports the expected PWM. Between retries it waits
`fanResponseDelay` (#512). A point that never matches is skipped. `fanResponseDelay` accepts sub-second values: a
plain number is seconds (`0.5`), a string can be a Go duration (`500ms`) (#542).

**Why**: Some controllers (e.g. NCT6687 with the nct6687d driver) ramp the output slowly and serve cached values.
Retrying after only `pwmSetDelay` (5 ms) made every point a mismatch, and the analysis failed in under a second while
the fans were spinning (#511). Sub-second values let fast hardware finish the analysis sooner. A plain number stays
seconds so existing configs keep working.

**Who benefits**: Users of slow embedded controllers (raise the delay) and of fast hardware (lower it).

**Trade-offs**: `fanResponseDelay` is a single global value; there is no per-fan override (unlike `pwmSetDelay`). A
skipped point in the start-boundary search moves the search upwards, so the result can be too high.

### Optional serialized initialization (`runFanInitializationInParallel`)

**Decision**: With `runFanInitializationInParallel: false`, only one controller at a time runs a part that moves a
fan: the whole `RunInitialization` and the `setPwmToGetPwmMap` sweep. The lock is a channel
(`initializationSequenceLock`), so a waiting controller can be cancelled. Nested calls don't wait again. Fans whose
data is saved don't take the lock, unless their `setPwmToGetPwmMap` is missing and the sweep runs
again. The default is `true` (parallel).

**Why**: Analyzing all fans at once stops all of them at the same time and heats up the system. Serializing keeps the
other fans under their previous control while one fan is measured. Before #549 the option had no effect: the lock was
only held in `computePwmMap`, which doesn't touch the hardware.

**Who benefits**: Systems with many fans or poor passive cooling (safety). Parallel analysis benefits users who want
the shortest first start.

**Trade-offs**: Serial analysis takes longer in total. The lock only covers one process: `fan2go fan init` running
next to the daemon is not serialized.

### Persistence: one bbolt file, opened per operation

**Decision**: Data is stored in a bbolt database at `dbPath` (default `/var/lib/fan2go/fan2go.db`). The daemon calls
`Persistence.Init`, which creates the parent directory (mode 0755); the file is created with mode 0600. Every
`Load`/`Save`/`Delete` opens the database, runs one transaction and closes it again. bbolt takes an exclusive
file lock on open and waits up to 1 minute (`bolt.Options{Timeout: 1 * time.Minute}`) before it gives up.

Data format: three buckets, keyed by fan `id`, values are JSON:

| Bucket                 | Content                                  | Example                       |
|------------------------|------------------------------------------|-------------------------------|
| `fans`                 | RPM curve, target PWM to RPM (or `null`) | `{"0":0,"1":0,...,"255":1510}`|
| `fanSetPwmToGetPwmMap` | set PWM to reported PWM                  | `{"0":0,"1":1,...}`           |
| `fanPwmMapping`        | `pwmMap`, an array of exactly 256 values | `[0,1,2,...,255]`             |

A fan without an RPM sensor stores `null` in `fans`: it counts as "analyzed, no curve". File and cmd fans store their
fixed linear curve (0..255 to 0..255).

**Why**: bbolt is an embedded, pure Go key-value store: no external service, one file, crash-safe transactions. Opening
it per operation lets the daemon and CLI commands (`fan init`, `fan reset`, `fan curve`) share the file, because no
process keeps the lock. JSON values are easy to inspect and tolerant of small type changes. The reasons for bbolt and
JSON aren't documented; this is the likely rationale.

**Who benefits**: Packagers (no service dependency, one path to configure), users who run CLI commands while the
daemon runs.

**Trade-offs**: Every access pays for opening the file and taking the lock; parallel controllers in one process
serialize on it. If another process keeps the file open for more than a minute, loads fail and the affected
controllers stop (see above). Data of fans that were removed from the config is never cleaned up.

### `fan2go fan init`: fresh data and hand-back

**Decision**: `fan2go fan --id <id> init` deletes the saved RPM curve, `setPwmToGetPwmMap` and `pwmMap` of the fan,
runs `RunInitializationAndRestore`, and prints the measured curve. It always hands the fan back afterwards: on success,
on errors, on SIGINT/SIGTERM and on panics (unless it failed before writing to the fan). Hand-back means the original
control mode and PWM, or `controlMode.onExit` (see [Fan controller](fan-controller.md)).
Flags: `--fan-response-delay` (`-e`) and `--assume-pwm-map-identity`. `fan2go fan --id <id> reset` only deletes
the data.

**Why**: Before #512, a failed or interrupted `fan init` left the fan in manual mode, in one case at PWM 255 (#511).
Before #549, a successful run left the fan in manual mode at the last PWM of the analysis while the daemon was stopped.

**Who benefits**: Users re-analyzing a fan by hand, especially without the daemon running: the fan never stays at an
arbitrary speed.

**Trade-offs**: The data is deleted before the analysis starts, so a failed `fan init` leaves the fan without data;
the daemon then analyzes it on its next start. A running daemon keeps using the curve it loaded and doesn't see the
new data until it is restarted or reloaded. Nothing stops `fan init` and the daemon from writing to the same fan at the
same time; stop the daemon first.

### No continuous re-analysis at runtime

**Decision**: The saved curve and the derived boundaries don't change while fan2go runs. The only runtime adaptation is
the `neverStop` stall detection: if a fan with `neverStop: true` stalls, the controller raises an in-memory offset on
`minPwm` by 1. It is not persisted and is lost on restart or reload (see [Fan controller](fan-controller.md)).
`Fan.UpdateFanRpmCurveValue` exists, but nothing calls it.

**Why**: The reason isn't documented. Likely: the curve is measured under controlled conditions (settle waits, median
of samples). Runtime RPM readings are taken under changing PWM values and would add noise. Raising `minPwm` only in
memory is a safe reaction that doesn't spread a transient stall into the saved data.

**Who benefits**: Predictable behaviour; the saved data only changes on an explicit analysis.

**Trade-offs**: Fan aging, dust or a changed fan aren't picked up. The `minPwm` offset is learned again after every
restart.

## Configuration

Global keys (defaults from `setDefaultValues` in `internal/configuration/config.go`):

| Key                              | Default                     | Used for                                                     |
|----------------------------------|-----------------------------|--------------------------------------------------------------|
| `dbPath`                         | `/var/lib/fan2go/fan2go.db` | bbolt database file; not reloadable (restart needed)         |
| `runFanInitializationInParallel` | `true`                      | `false` analyzes one fan at a time (the example `fan2go.yaml` sets `false`) |
| `fanResponseDelay`               | `2` (seconds)               | wait between readback retries and after small steps; number = seconds, or a duration string |
| `maxRpmDiffForSettledFan`        | `20`                        | lower bound for the allowed mean drift when waiting for a fan to settle |
| `analysis.coarseStep`            | `16`                        | step of the max-boundary scan; interior uses `max(2 × coarseStep, 8)` |
| `analysis.sampleCount`           | `3`                         | RPM samples per point (median), must be >= 1                 |
| `analysis.sampleDelay`           | `200ms`                     | time between samples                                         |
| `analysis.settleTimeout`         | `30s`                       | max wait for a fan to settle                                 |
| `fanController.pwmSetDelay`      | `5ms`                       | wait between writing and reading back PWM (sweep and analysis) |

Per-fan keys:

| Key                 | Default      | Used for                                                          |
|---------------------|--------------|-------------------------------------------------------------------|
| `pwmMap`            | `autodetect` | `identity`, `linear`, `values` skip detection and the database    |
| `setPwmToGetPwmMap` | `autodetect` | `identity`, `linear`, `values` skip the sweep and the database    |
| `minPwm`, `startPwm`, `maxPwm` | measured | override the values derived from the curve               |
| `pwmSetDelay`       | global value | per-fan override of `fanController.pwmSetDelay`                   |

CLI: `fan2go fan --id <id> init [--fan-response-delay <delay>] [--assume-pwm-map-identity]`,
`fan2go fan --id <id> reset`, `fan2go fan --id <id> curve`.

## Known limitations and open questions

- The derived `maxPwm` is effectively always 255. The cleanup makes the curve monotonic, so PWM 255 always has the
  peak RPM and is the highest value with at least 95% of the peak. A fan that saturates at PWM 150 still gets
  `maxPwm` 255, and the upper part of the curve range changes little. The max-boundary search only decides where
  interior sampling stops. The config comment ("the highest PWM value that yields an RPM increase") describes a
  different rule. Set `maxPwm` by hand if this matters.
- `minPwm` and `startPwm` are not measured separately (#30). The binary search mixes start-from-standstill and
  keep-spinning behaviour.
- A fan that spins at PWM 0 gets RPM 0 stored for PWM 0 anyway, and its `startPwm` becomes the next measured value.
- Saved data is keyed by fan `id` and not invalidated when the hardware or the `pwmMap` config changes.
- A `null` curve (stored when the RPM sensor was missing at analysis time) counts as "analyzed". If the RPM input
  appears later (fixed `rpmChannel`, driver loaded late), the fan keeps running without a curve until `fan init`.
- The autodetected `pwmMap` ignores the readback values; quantized drivers get an identity map with many redundant
  measurement points.
- `runFanInitializationInParallel` and the database lock only coordinate within one process; `fan init` next to a
  running daemon is not prevented.
- A running daemon doesn't pick up data written by `fan init` until restart or reload.
- A shutdown cancels a running analysis. The fan is handed back and no RPM curve is saved, so the next start measures
  it again from the beginning. A config reload (SIGHUP or a saved config file) doesn't: it is deferred until no fan is
  analyzed anymore, so the new config only takes effect after the analysis. See [Daemon](daemon.md#config-reload).
- `dbPath` changes need a restart: the daemon creates its persistence once (`RunDaemon`) and reuses it on reload
  (a reload logs a warning if `dbPath` changed).
- `fanResponseDelay` has no per-fan override; the settle sample interval (500 ms), the 50 RPM noise threshold and the
  95% max ratio are hard-coded.
- The RPM curve is never updated at runtime; the `neverStop` `minPwm` offset is not persisted.
- Entries of fans removed from the config stay in the database.
- The CLI commands (`fan init`, `fan reset`) don't create the database directory. On a system where the daemon never
  ran, they fail with "no such file or directory".
