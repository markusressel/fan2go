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
 │        ├─ missing / corrupt / no curve although the fan has an RPM    │
 │        │  sensor (hwmon, nvidia) ─► RunInitialization                 │
 │        │      lock (if runFanInitializationInParallel: false)         │
 │        │      computeFanSpecificMappings                              │
 │        │         setPwmToGetPwmMap: config | formula | db |              │
 │        │                            probe readback delay + sweep 0..255│
 │        │         pwmMap:            config | db | one value per level  │
 │        │      FanCurveAnalyzer.RunInitializationSequence              │
 │        │         start boundary ─► max boundary ─► interior ─► cleanup│
 │        │      AttachFanRpmCurveData, SaveFanRpmData,                  │
 │        │      SaveFanAnalysisInfo (quirk, readback delay)             │
 │        ├─ missing (file, cmd fan) ─► save the fixed linear curve      │
 │        └─ other error (e.g. db locked) ─► stop with an error          │
 ├─ AttachFanRpmCurveData  ◄────────────────────────────────────────────┘
 │      derives startPwm, maxPwm (and minPwm = startPwm) unless configured
 ├─ computeFanSpecificMappings   (loads the saved mappings; sweeps again if
 │                                 setPwmToGetPwmMap is missing or corrupt)
 ├─ alignDerivedMaxPwmWithOutputLevels, loadReadbackDelay
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
it switches the fan to manual mode (`takeManualControl`; if that fails, the analysis fails, see
[fan-controller.md](fan-controller.md)) and runs four phases over the distinct target values:

1. **Start boundary** (`detectStartBoundary`): binary search for the lowest target value at which the fan spins.
   "Spinning" means at least 50 RPM (`rpmNoiseThreshold`, `fans.IsRpmLikelySpinning`). Each step waits for the fan to
   settle, up to `analysis.settleTimeout`. Every non-spinning probe is stored as 0 RPM. If a probe is skipped
   because the fan doesn't report the expected PWM (see below), the search tries the next higher and then the next
   lower value in its window first; only if all three are skipped, it continues above the probe. If no value spins,
   the analysis fails with "unable to detect start boundary, no spinning PWM found".
2. **Max boundary** (`detectMaxBoundary`, `confirmMaxBoundary`): a fast scan of the top distinct values (the top 65,
   or from the start boundary if that is higher) in steps of `analysis.coarseStep`, each with a plain
   `fanResponseDelay` wait. It then walks down from the top, measuring every value not yet measured, until one reaches
   95% of the peak RPM. It re-measures the top four values and the neighbours of that candidate with settle waits,
   and picks the highest value that reaches 95% of the confirmed peak. This boundary only decides where interior
   sampling stops; the derived `maxPwm` is computed from the cleaned-up curve (see below).
3. **Interior** (`sampleInteriorCoarse`): measures every `max(2 * analysis.coarseStep, 8)`-th distinct value between
   the two boundaries, with settle waits for jumps of 8 or more target values (in practice always, since the step is
   at least 8 distinct values).
4. **Cleanup** (`rpmCurveMeasurementCleanup`): sets every sample below the noise threshold, and everything below the
   first spinning sample, to 0, adds anchors at PWM 0 and 255, fills the range below the start PWM with step
   interpolation and the rest with linear interpolation, makes the curve monotonically increasing, and smooths the range
   between the derived start PWM and 255 (both exclusive) with a Kalman filter. The start PWM and PWM 255 keep their
   values.

Each point is measured by `measureAtPwm`:

- Write the target value, wait `pwmSetDelay`, then read the PWM back every 5 ms until it matches the expected value
  from the `setPwmToGetPwmMap`, for up to `fanResponseDelay`. On a mismatch, write it again, up to 3 writes in total.
  If it still doesn't match, the point is skipped (not an error). The time until the readback matched is recorded
  as an observed readback delay (see below).
- Wait: `waitForFanToSettle` (if a settle timeout is given) or a plain `fanResponseDelay` sleep.
- Take `analysis.sampleCount` RPM samples, `analysis.sampleDelay` apart, and use their median.

`waitForFanToSettle` reads the RPM every 500 ms, runs it through a Kalman filter and keeps the last 8 values. After 6
values it checks drift of the mean, change of the noise range and the trend. The noise range is the range of the
values around their linear trend (`detrendedRange`), so a fan that still ramps steadily doesn't count as noisy (and
so as settled). It needs 3 stable checks in a row. If
`analysis.settleTimeout` runs out first, it logs a warning and measures anyway. So a settled point takes at least about
4 seconds.

### How the result is used

`AttachFanRpmCurveData` derives the boundaries from the stored curve with `ComputePwmBoundariesFromCurveData`:

- `startPwm`: the lowest target value with at least 50 RPM.
- `maxPwm`: the lowest target value (at or above `startPwm`) with at least 98% of the peak RPM
  (`fans.MaxPwmRpmRatio`), 255 if there is none.
- `minPwm`: set to `startPwm` (there is no separate measurement yet, see #30).

After the PWM mappings are known, `alignDerivedMaxPwmWithOutputLevels` checks the derived `maxPwm` against the output
levels of the fan: target values whose raw values read back the same are one level. The curve between two measured
points is interpolated, so inside a level it can reach 98% although the level doesn't (8 levels: the curve reaches
98% inside level 6, which runs at 89%). The curve value at the first target value of a level counts for the whole
level, and `maxPwm` is raised to the first level that reaches 98% (255 if none).

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
then. Only a change of the [known hardware quirks](hardware-quirks.md#persistence) entry of a hwmon fan deletes the
data automatically.

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

- `setPwmToGetPwmMap`: if the fan has a [known hardware quirks](hardware-quirks.md) entry with a readback formula
  (and `quirks.enabled` is not `false`), the map comes from that formula, without a
  sweep and without the database. Otherwise `computeSetPwmToGetPwmMapAutomatically` measures the readback delay
  (`probeReadbackDelay`, see below), then writes every value 0..255 and reads `pwm` back until it changes, for up to
  twice the measured delay (at least `pwmSetDelay`). Values whose write fails are left out. Without a readable `pwm`
  (or with `fan init --assume-pwm-map-identity`), it assumes a 1:1 map.
- `pwmMap`: `computePwmMapAutomatically` groups the keys of the `setPwmToGetPwmMap` (the raw values the driver
  accepted) by the value they read back: each group is one output level of the fan (`outputLevelValues`). Of each
  group it uses the value that reads back as itself, else the lowest one, and spreads these values evenly over
  0..255 as steps (`util.ExpandMapToFullRange`). If the driver accepts and reports all values, this is the identity.
  If it only accepts e.g. 0..100, or has 8 levels, the target range 0..255 is divided between those values. For a
  [known hardware quirks](hardware-quirks.md) formula, the groups are the entry's `OutputLevels()`. The grouping is
  not used (every accepted value is used, like before) if the readback decreases somewhere, has fewer than two
  values, or the fan is an nvidia fan (its `minPwm`/`maxPwm` are percent values of the identity map).

**Why**: Some drivers don't report the value that was written (e.g. they report 0..100 or round), others only accept
part of the range. Mixing both into one map broke the third-party check (#371). Without the readback map, every
analysis write would look like a mismatch.

**Who benefits**: Users of unusual hardware (#213, #511). Users whose hardware is known can skip the sweep with
`identity` or set exact values.

**Trade-offs**: The sweep briefly writes every value from 0 to 255 in about 1.3 seconds (256 × 5 ms), including 0,
plus 4 writes of 0 and 255 to measure the readback delay. On a driver that caches its readback, the sweep takes
longer (about 256 × the cache period). The grouped `pwmMap` relies on the readback: a driver that reports every
value but has fewer real speeds (the readback doesn't show the levels) still gets an identity `pwmMap`.

The grouped `pwmMap` (since 1.0.0) changes which raw value a target value writes on quantized drivers: a target
range selects a level, instead of the raw value with the same number. Configured `minPwm`/`startPwm`/`maxPwm` are
target values, so on such a fan they select another raw value than before. Saved `pwmMap`s are kept: only a new
analysis (`fan init`, `fan reset`, or a changed known hardware quirks entry) computes the grouped map.

### `maxPwm` is the lowest PWM with 98% of the peak RPM

**Decision**: The derived `maxPwm` is the lowest target value whose RPM is at least 98% of the peak RPM of the
curve, raised to the first output level that reaches it (see "How the result is used"). The fail-safe paths
(`onSensorFailure: max`, and the `neverStop` stall check that stops the controller "at max") don't use it: they use
the configured `maxPwm`, or full speed (255; nvidia: 100) without one (`fans.FailSafeMaxPwm`).

**Why**: Before 1.0.0, `maxPwm` was the *highest* value with 95% of the peak. The cleanup makes the curve monotonic,
so that was always 255, and a fan that saturates at PWM 150 used the upper 40% of the curve range for no change in
speed. The ratio was chosen with a simulation of the full analysis (`RunInitializationSequence` with median of 3
samples, interpolation, monotonic fill and Kalman smoothing), 30 runs per fan model and tach noise of 1% and 3%:

| Fan model, noise | 95%: RPM at `maxPwm` (worst) | 98%: RPM at `maxPwm` (worst) | 98%: `maxPwm` (median) |
|---|---|---|---|
| linear, 1% | 95.1% | 98.8% | 255 |
| linear, 3% | 93.5% | 96.7% | 255 |
| concave (`pwm^0.6`), 3% | 93.1% | 96.3% | 255 |
| saturates at PWM 150, 1% | 100% | 100% | 168 |
| saturates at PWM 150, 3% | 100% | 100% | 179 |
| soft saturation (`1-e^(-pwm/35)`), 3% | 95.5% | 97.8% | 214 |
| 101 levels (Corsair formula), 3% | 91.9% | 95.9% | 255 |
| 8 levels, with level alignment | 89.3% (without alignment) | 100% | 255 (top level from 224) |

With 95%, fans that don't saturate lose up to 5-7% of their top speed at the highest curve value, about twice the
tach noise. 98% keeps that loss within the noise (1-3%) and still cuts off most of the flat top of a saturating fan.
The Kalman filter lags behind a rising curve, so smoothing can only move `maxPwm` up (towards full speed). Without
the level alignment, the interpolated curve of a fan with 8 levels reaches 98% inside the second highest level, and
the fan would never reach its top level.

The fail-safe paths need full speed, not "98% of it": a derived value can be wrong (noise, a fan that was replaced),
and the stall check shouldn't give up below full speed.

**Who benefits**: Fans that saturate early: the whole curve range changes the speed. Everybody: the fail-safe speed is
full speed again, independent of the measurement.

**Alternatives considered**: 95% (the old constant): loses too much top speed on linear fans. 99%: the threshold is within the
noise, so the result varies much more (fan saturating at 150, 3% noise: 169 to 255, median 199). Measuring `maxPwm` separately (a slow descent from 255): slower, and the
boundary search already measures the top range.

**Trade-offs**: At the highest curve value, a fan that doesn't saturate runs up to about 2-3% slower than before
(the measured 98% plus noise). Saved curves are kept, so the new `maxPwm` applies on the next start without a new
analysis. A fan whose output is quantized but whose readback isn't (the levels are unknown) can still get a
`maxPwm` inside its second highest level; set `maxPwm` for such a fan. `fan2go fan curve` shows the value derived
from the curve, before the level alignment.

### Measure the readback delay

**Decision**: fan2go measures how long a fan takes to report a written PWM value, during the analysis:

- Before the sweep, `probeReadbackDelay` writes 0 and 255 alternately (4 writes, the first one far from the current
  value) and polls the readback every 5 ms until it changes, up to 2 s each.
- The sweep waits for each value until the readback changes (several values can have the same level), up to twice
  the longest observed delay, at least `pwmSetDelay`.
- `measureAtPwm` polls until the readback matches (see above) and records the time.

The longest delay observed (capped at 2 s, rounded up to ms) is saved as `readbackDelayMillis` in the `fanAnalysisInfo`
record ([details](hardware-quirks.md#persistence)), for fans whose PWM can be read back (not nvidia fans: fan2go
doesn't use their reported value). A delay of 0 means the first read
after `pwmSetDelay` already showed the new value. The daemon loads it (`loadReadbackDelay`). While it controls the
fan, for `max(readbackDelay, pwmSetDelay)` after a write (`readbackMayBeStale`):

- the `pwmValueChangedByThirdParty` check is skipped, because the fan may still report the old value;
- `setTargetPwm` compares the target with the value it wrote last instead of the readback. Before, a write of A, B,
  A within the delay read back A (the old value) for the second A and skipped it, so B stayed.

Without a measured delay (0, or a record without the field), all of this behaves like before.

**Why**: Some drivers report a written value only after a while. Older versions of the out-of-tree `nct6687`
driver cache register values for 1 s (#272, #358), and `pwmSetDelay: 200ms` helped a user with an unknown driver
(#378). With a fixed 5 ms wait, the sweep recorded the previous value for every write, and the control loop reported
third-party changes that were its own writes. The hwmon name doesn't tell old and new driver versions apart, so a
[known hardware quirks](hardware-quirks.md) entry can't describe it.

**Who benefits**: Users of drivers with a slow or cached readback: a correct `setPwmToGetPwmMap` and `pwmMap`, no
false warnings, no lost writes. Users of fast drivers: no change, the sweep still takes about 1.3 s.

**Alternatives considered**: A fixed, longer `pwmSetDelay` (slows every fan down; still the manual floor). A
registry field with a fixed delay per device (not possible without a primary source; may be added later if
measuring can't catch a device's delay).

**Trade-offs**: The delay of a caching driver depends on the phase of the cache, so the measurement can be shorter
than the cache period; the sweep waits twice as long, and the control loop only uses the longest observation. A
readback that never changes (e.g. some amdgpu cards, #148) can't be measured: the analysis logs a warning and uses
`pwmSetDelay`. Data analyzed by an older version has no measured delay until the next `fan init`. A ramping
controller (the readback moves towards the value over seconds) reports the time until the first change in the
probe, and the time until it matches in `measureAtPwm` (up to 2 s). With such a delay, the `pwmValueChangedByThirdParty`
check is skipped for that long after every write, so with a target that changes on most cycles it rarely runs. Only
the analysis saves the delay: a sweep that runs at startup because only the saved `setPwmToGetPwmMap` is missing
(e.g. it was corrupt) measures it, but doesn't save it.

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
skipped point in the start-boundary search makes the search try its neighbours first; if they are skipped too, the
search moves upwards, so the result can be too high.

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

**Trade-offs**: Serial analysis takes longer in total. The lock only covers one process; `fan2go fan init` can't run
next to the daemon anyway (see "`fan2go fan init`" below).

### Persistence: one bbolt file, opened per operation

**Decision**: Data is stored in a bbolt database at `dbPath` (default `/var/lib/fan2go/fan2go.db`). The daemon calls
`Persistence.Init`, which creates the parent directory (mode 0755); the file is created with mode 0600. Every
`Load`/`Save`/`Delete` opens the database, runs one transaction and closes it again. bbolt takes an exclusive
file lock on open and waits up to 1 minute (`bolt.Options{Timeout: 1 * time.Minute}`) before it gives up.

Data format: five buckets, keyed by fan `id`, values are JSON:

| Bucket                 | Content                                  | Example                       |
|------------------------|------------------------------------------|-------------------------------|
| `fans`                 | RPM curve, target PWM to RPM (or `null`) | `{"0":0,"1":0,...,"255":1510}`|
| `fanSetPwmToGetPwmMap` | set PWM to reported PWM                  | `{"0":0,"1":1,...}`           |
| `fanPwmMapping`        | `pwmMap`, an array of exactly 256 values | `[0,1,2,...,255]`             |
| `fanOriginalState`     | state before fan2go took control, only while it controls the fan | `{"hardware":"hwmon:nct6798/0/pwm1","controlMode":2,"automaticControlMode":5,"pwm":120}` |
| `fanAnalysisInfo`      | known hardware quirks entry the analysis data of a hwmon fan was measured with ([details](hardware-quirks.md#persistence)), and the measured readback delay (fans whose PWM can be read back) | `{"quirk":"corsaircpro","quirkVersion":1,"readbackDelayMillis":0}` |

A fan without an RPM sensor stores `null` in `fans`: it counts as "analyzed, no curve" as long as the fan has no
RPM sensor. If a hwmon or nvidia fan has one later (a fixed `rpmChannel`, a driver that was loaded late), the `null`
curve counts as missing and the fan is analyzed again. File and cmd fans store their fixed linear curve (0..255 to
0..255).

A database is used by one fan2go process at a time: the daemon, `fan init` and `fan reset` take an advisory lock
(`flock`) on `<dbPath>.lock` and refuse to run if another process holds it (`persistence.AcquireProcessLock`). The
file contains the PID of the holder for the error message. The kernel releases the lock when the process exits, also
after `SIGKILL`, so a lock file left behind never blocks and needs no stale-PID check. Read-only commands
(`fan curve`) don't take it.

**Why**: bbolt is an embedded, pure Go key-value store: no external service, one file, crash-safe transactions. Opening
it per operation lets the daemon and CLI commands (`fan init`, `fan reset`, `fan curve`) share the file, because no
process keeps the lock. JSON values are easy to inspect and tolerant of small type changes. The reasons for bbolt and
JSON aren't documented; this is the likely rationale.

**Who benefits**: Packagers (no service dependency, one path to configure), users who run CLI commands while the
daemon runs.

**Trade-offs**: Every access pays for opening the file and taking the lock; parallel controllers in one process
serialize on it. If another process keeps the file open for more than a minute, loads fail and the affected
controllers stop (see above). Analysis data of fans that were removed from the config is never cleaned up.
`fanOriginalState` is not analysis data: it is described in [fan-controller.md](fan-controller.md), uses a 5 s lock
timeout, and its entries of removed fans are deleted at daemon start.

### `fan2go fan init`: fresh data, replaced on success, and hand-back

**Decision**: `fan2go fan --id <id> init` measures the fan again with `RunInitializationAndRestore`: the
`setPwmToGetPwmMap` (unless it comes from the config or a known readback formula), the `pwmMap` and the RPM curve.
It doesn't use the saved data of the fan (`freshAnalysis`), and doesn't change it until the analysis succeeded. Then
it deletes the saved RPM curve, `setPwmToGetPwmMap`, `pwmMap` and analysis info, and saves the new ones
in one transaction (`Persistence.ReplaceFanAnalysisData`). A failed or interrupted run, or a failed save, keeps the saved
data. It prints the measured curve. It always hands the fan back afterwards: on success, on errors, on SIGINT/SIGTERM
and on panics (unless it failed before writing to the fan). Hand-back means the original control mode and PWM, or
`controlMode.onExit` (see [Fan controller](fan-controller.md)). Like the daemon, it saves the original state in
`fanOriginalState` while it controls the fan, uses a saved one (left behind by a killed daemon or `fan init`) instead
of the current state, and deletes it after a complete hand-back. `reset` doesn't touch it. Flags:
`--fan-response-delay` (`-e`) and `--assume-pwm-map-identity`. `fan2go fan --id <id> reset` only deletes the data.

Both commands create the database directory (`Persistence.Init`), and both refuse to run while the daemon (or
another `fan init`/`reset`) uses the same database: "another fan2go process (PID 1234) uses the fan2go database
(lock file /var/lib/fan2go/fan2go.db.lock). Is the fan2go daemon running? Stop it first (e.g. 'sudo systemctl stop
fan2go'), then run this command again". The daemon refuses to start while one of them runs.

Errors saving the result are returned (the analysis fails), in `fan init` and in the daemon. Before, the daemon only
logged a failed save and then failed with a confusing "not exist" when it loaded the data again.

**Why**: Before #512, a failed or interrupted `fan init` left the fan in manual mode, in one case at PWM 255 (#511).
Before #549, a successful run left the fan in manual mode at the last PWM of the analysis while the daemon was stopped.
Before 1.0.0, `fan init` deleted the data before it measured, so a failed or interrupted run lost valid data and the
next start ran an unexpected analysis. It could also run next to the daemon: both wrote to the same fan, the
measurement was garbage, and the daemon kept its old data. The API can't be used to detect the daemon, because it is
off by default. On a system where the daemon never ran, the commands failed with "no such file or directory".

**Who benefits**: Users re-analyzing a fan by hand, especially without the daemon running: the fan never stays at an
arbitrary speed, and the old data survives a failed run.

**Trade-offs**: The daemon has to be stopped for `fan init` and `fan reset`, also to analyze a fan it doesn't control.
The daemon (which analyzes only fans without usable data) saves the curve and the analysis info in two transactions;
if it is killed in between, the record is missing, and a fan with a known hardware quirks formula is analyzed again.

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
| `fanController.pwmSetDelay`      | `5ms`                       | minimum wait between writing and reading back PWM (sweep and analysis); the measured readback delay is used if longer |

Per-fan keys:

| Key                 | Default      | Used for                                                          |
|---------------------|--------------|-------------------------------------------------------------------|
| `pwmMap`            | `autodetect` | `identity`, `linear`, `values` skip detection and the database    |
| `setPwmToGetPwmMap` | `autodetect` | `identity`, `linear`, `values` skip the sweep and the database    |
| `minPwm`, `startPwm`, `maxPwm` | measured | override the values derived from the curve               |
| `pwmSetDelay`       | global value | per-fan override of `fanController.pwmSetDelay`                   |
| `quirks.enabled`    | `true`       | `false` doesn't apply the [known hardware quirks](hardware-quirks.md) of the device |

CLI: `fan2go fan --id <id> init [--fan-response-delay <delay>] [--assume-pwm-map-identity]`,
`fan2go fan --id <id> reset`, `fan2go fan --id <id> curve`.

## Known limitations and open questions

- `minPwm` and `startPwm` are not measured separately (#30). The binary search mixes start-from-standstill and
  keep-spinning behaviour.
- A fan that spins at PWM 0 gets RPM 0 stored for PWM 0 anyway, and its `startPwm` becomes the next measured value.
- Saved data is keyed by fan `id` and not invalidated when the hardware or the `pwmMap` config changes (only a
  changed known hardware quirks entry invalidates it).
- A fan whose output is quantized but whose readback isn't gets an identity `pwmMap`, and its derived `maxPwm` can be
  inside its second highest level. Set `maxPwm` (and a `pwmMap`) for such a fan.
- Saved `pwmMap`s from before 1.0.0 are kept; quantized drivers get the grouped map only after a new analysis.
- The readback delay is measured only during an analysis; data from older versions has none until `fan init`.
- The derived `maxPwm` cuts off the top 2% of the speed of fans that don't saturate.
- A running daemon doesn't pick up data written by `fan init` until restart or reload; it has to be stopped for
  `fan init` anyway.
- A shutdown cancels a running analysis. The fan is handed back and no RPM curve is saved, so the next start measures
  it again from the beginning. A config reload (SIGHUP or a saved config file) doesn't: it is deferred until no fan is
  analyzed anymore, so the new config only takes effect after the analysis. See [Daemon](daemon.md#config-reload).
- `dbPath` changes need a restart: the daemon creates its persistence once (`RunDaemon`) and reuses it on reload
  (a reload logs a warning if `dbPath` changed).
- `fanResponseDelay` has no per-fan override; the settle sample interval (500 ms), the 50 RPM noise threshold, the
  98% max ratio and the 2 s readback delay cap are hard-coded.
- The RPM curve is never updated at runtime; the `neverStop` `minPwm` offset is not persisted.
- Entries of fans removed from the config stay in the database.
