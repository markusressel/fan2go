# Control algorithms

A control algorithm decides how a fan moves from its current PWM value to the value its curve asks for. fan2go
offers two: `direct` (apply the curve value, optionally smoothed by a time constant, ramp rates and a deadband) and
`pid` (smooth the curve value with a PID loop). The default is `direct` with `timeConstant: 2s` and `deadband: 1%`.
The algorithms live in `internal/control_loop` (`DirectControlLoop`, `PidControlLoop`), the PID math in
`internal/util/pid.go` (`PidLoop`), the default in `internal/configuration/fans.go`
(`DefaultControlAlgorithmConfig`, `FanConfig.GetControlAlgorithm`), and the selection in `internal/factory.go`
(`createControlLoop`).

## How it works

Every cycle (`fanController.adjustmentTickRate`, default `200ms`) the curve evaluator evaluates all curves once and
notifies each fan controller (see [curves.md](curves.md)). The fan controller then runs `UpdateFanSpeed` (see
[fan-controller.md](fan-controller.md) for everything after the control algorithm):

```
curve evaluator (every adjustmentTickRate)
        |
        v  curve.CurrentResult()
DefaultFanController.calculateTargetSpeed
        |  result.Err != nil  --> handleCurveFailure (onSensorFailure), control loop NOT cycled
        |  recovering from max --> controlLoop.Preset(maxPwm curve value); from restore --> controlLoop.Reset()
        |
        v  controlLoop.Cycle(now, curveValue)   [0..255] in, [0..255] out
DefaultFanController.UpdateFanSpeed
        |  scale [1..255] to [minPwm..maxPwm], neverStop, stall check
        v
setTargetPwm --> pwmMap --> fan.SetPwm
```

The control algorithm works in the curve value space `[0..255]`. It runs before the value is scaled to the fan's
`minPwm`..`maxPwm` range and before `neverStop` and the `pwmMap` are applied. It never sees the fan's RPM or the PWM
value read back from the hardware.

The interface (`internal/control_loop/common.go`):

```go
type ControlLoop interface {
	Cycle(now time.Time, target float64) float64
	Reset()
	Preset(value float64)
}
```

- `Cycle` advances the loop. `now` comes from the fan controller's clock (`getClock().Now()`), so a fake clock in
  tests drives the controller and its algorithm together.
- `Reset` makes the next `Cycle` start at its target, like the first cycle of a new instance.
- `Preset(value)` makes the next `Cycle` continue from `value` (clamped to `[0..255]`) instead of the last output.
  The fan controller uses it when it set the fan without the loop (see "The control loop continues from the fan's
  speed after a curve failure").

Each fan controller owns one `ControlLoop` instance, used only by its update loop (not safe for concurrent use).
A new instance is created on every start and every successful config reload, so all algorithm state starts over
then. If a reload fails after the old controllers were stopped (an error while creating the new objects; an invalid
config doesn't stop them), fan2go restarts the old fan controllers with their old `ControlLoop` instances, and `Run`
resets them first (`ControlLoop.Reset`), so the state starts over as well (see [fan-controller.md](fan-controller.md)
for the other state that is reset then).

### Direct

`DirectControlLoop.Cycle(now, target)` clamps the target to `[0..255]` and then:

- First cycle (and the first cycle after `Reset`): return the target. The low-pass state starts there too.
- `Preset(value)` sets the output and the low-pass state to `value` and makes the next cycle count as no time. So
  with smoothing, the next cycle returns `value` and smoothing continues from it; with `maxPwmChangePerCycle`, the
  next step starts from `value`; without options, the next cycle applies its target.
- Later cycles, with `dt` = the time since the last cycle (`0` if the clock went backwards):
  1. **Low-pass** (`timeConstant` τ > 0): `filtered += (1 - exp(-dt/τ)) * (target - filtered)`, then
     `filtered = target` once they are less than `0.5` apart. Without a time constant, `filtered = target`.
  2. **Deadband** (`deadband` > 0): if `|filtered - output| < deadband`, the output doesn't change. Except when the
     new value is `0` or `255`, or when it is on the other side of `1` than the output (below `1`, a fan without
     `neverStop` stops, see [fan-controller.md](fan-controller.md)).
  3. If moving to `filtered` would move the output away from the target, the output doesn't change.
  4. **Rate limit**: the output moves towards `filtered` by at most `rampUp x dt` upwards and `rampDown x dt`
     downwards (`0` = no limit). With the deprecated `maxPwmChangePerCycle`, by at most that per cycle in both
     directions instead.

Without options (explicit `controlAlgorithm: direct`), every cycle returns the clamped target.

### PID

`PidControlLoop.Cycle`:

- First cycle (and the first cycle after `Reset`): return the target, clamped to `[0..255]`, and call
  `PidLoop.PresetAt(now, target, target)`.
- First cycle after `Preset(value)`: the same with `value` instead of the target.
- A cycle more than `max(2s, 5 x adjustmentTickRate)` after the last one (a gap), or before it (the clock went
  backwards): the gap counts as `max(1s, adjustmentTickRate)` (`PidLoop.ResumeAt`), then a normal step.
- Other cycles: `PidLoop.LoopAt(now, target, measured)` with `measured = lastPidOutput`, the previous output of
  this same loop. The result (clamped to `[0..255]` by `PidLoop`) becomes the new `lastPidOutput`.

`PidLoop.LoopAt` computes, with `dt` the time since the last call (`now` from the controller's clock; with the
real clock a monotonic reading, so suspend doesn't count):

```
err      = target - measured
output   = p*err + i*integral - d*(measured - lastMeasured)/dt     (clamped to [0..255])
integral = integral + err*dt     (skipped while the output is saturated and err pushes further,
                                  then i*integral is clamped to [0..255])
```

If `dt <= 0`, the last output is returned unchanged.

## Design decisions

### The PID algorithm is a smoothing filter, not a feedback loop on the fan

**Decision**: `PidControlLoop` feeds its own last output back as the measured value
(`floatMeasured := l.lastPidOutput`). It doesn't read the fan's RPM or PWM.

**Why**: The goal is to avoid abrupt speed changes when the curve value jumps (for example, a CPU temperature that
spikes for a second). With the measured value equal to the previous output, the loop is a discrete low-pass filter:
at steady state the output comes from the I term alone, and a change of the curve value is followed with a time
constant of about `1/i` seconds. The README describes it this way since #554. Before #554 it listed wrong default
values (`P=0.3, I=0.02, D=0.005`), which the code hasn't used since the loop rewrite in #361.

The quantity a fan controller could measure is RPM, but RPM and PWM are in different units, the relation between
them is non-linear and fan-specific, and many fans have no RPM sensor (`cmd` and `file` fans, some hwmon fans).
The code doesn't document why a filter was chosen over RPM feedback; these are the likely reasons.

**Who benefits**: Users of the `pid` algorithm (the default before 1.0.0): noisy or spiky sensors don't make the fans
audibly hunt. Users who want a real temperature feedback loop use a `pid` curve instead (see [curves.md](curves.md)). That curve
measures a sensor, has a setpoint, and is unrelated to this control algorithm. With a `pid` curve and the `pid`
control algorithm (or the default algorithm, which has a time constant), the filter adds its lag behind the curve's
feedback loop.

**Alternatives considered**:

- Linear ramp (`direct` with `maxPwmChangePerCycle`). It was added in #267 (issue #266) as a replacement for the
  PID approach, which at that time didn't reach its targets. Both were kept, and the user picks one per fan.
- A time-based low-pass (`direct` with `timeConstant`). Added for 1.0.0 and the default since then (see below).
- RPM feedback. Not implemented.

**Trade-offs**:

- The name "PID" suggests feedback on the fan. It is not. The loop can't correct a fan that runs faster or slower
  than expected.
- `p` and `d` have little effect with the default values; `i` sets the speed. This isn't obvious from the config.
- The filter behaviour depends on the cycle rate (next decision).

### PID behaviour with the default values, and its dependence on the cycle rate

**Decision**: The defaults are `p: 0.05`, `i: 0.4`, `d: 0.01` (`DefaultPidConfig` in
`internal/control_loop/pid.go`). `dt` is the measured time between two cycles, but the loop is advanced only once
per cycle, so the result depends on `adjustmentTickRate`.

**Why**: The values were chosen in #361 ("lower pids even more"). The reasoning behind the exact numbers isn't
documented. A simulation of the real `PidLoop` (step from 50 to 200, default values) gives:

| `adjustmentTickRate` | Behaviour |
|---|---|
| `100ms` - `500ms` | No overshoot. Time constant about 2.3 s at `200ms` (63% of a step after about 2.6 s, because the first cycle after a step only adds the P term). Within 2 PWM after about 10 s. |
| about `700ms` | Overshoot starts (very small at first). |
| `800ms` | Overshoot of about 1.5 PWM. |
| `1s` | Overshoot of about 12 PWM (peak 211.6 for a target of 200). |
| `2s` | Swings up to 255; takes about 50 s to settle. |
| about `2.64s` and slower | Unstable. The output oscillates between the limits and never settles. |

The reason is the discrete update: the I term changes the output by `i * dt * err` per cycle. Once `i * dt`
approaches 1, one cycle overcorrects. The D term acts as `d / dt` on the change of the loop's own output, so a large
`d` also causes overshoot (`d: 0.5` reaches 255 for a step to 200 at `200ms`).

**Who benefits**: Users on the default `200ms` cycle get a smooth, overshoot-free response.

**Trade-offs**: Users who raise `adjustmentTickRate` (for example, to reduce CPU wakeups) change the behaviour of
every fan that uses the `pid` algorithm. The README says to lower `i` accordingly. Validation rejects unstable
combinations and warns about overshoot (next decision).

### Validation computes the stability of the PID algorithm

**Decision** (maintainer decision): Validation rejects a `pid` algorithm whose gains are unstable with the
configured `adjustmentTickRate`, and warns if it overshoots a change of the curve value by more than 1 %
(`control_loop.AnalyzePidStability`, called by `validatePidControlAlgorithms` after the global timing checks). It
checks every fan that uses the `pid` algorithm: `controlAlgorithm.pid`, the string `pid` and the deprecated
`controlLoop`. Fans without `controlAlgorithm` use `direct` (since 1.0.0) and are not checked. `i` must be `> 0`.

**Why**: The algorithm feeds its own output back, so inside `[0..255]` it is a linear discrete system that doesn't
depend on the fan. With the curve value `r`, the output `y`, the I term `u` and `T = adjustmentTickRate`:

```
y[k] = p*(r - y[k-1]) + u[k-1] - (d/T)*(y[k-1] - y[k-2])
u[k] = u[k-1] + i*T*(r - y[k-1])
```

The characteristic polynomial is `z³ + (p + d/T - 1)z² + (iT - p - 2d/T)z + d/T`. The loop is stable if all
roots are strictly inside the unit circle (checked with the Jury criterion). `P(1) = iT`, so `i <= 0` is never
stable: without an I term the output settles at `p / (1 + p)` of the curve value. The overshoot is measured on the
simulated step response from a settled state, which is what the bumpless start gives. With the default values the
loop is stable above about `21ms` and below about `2.64s`; below that, `d/T` is too large (a pole below `-1`),
above that `iT` is (complex
poles outside the unit circle). Overshoot above 1 % starts just above `800ms`.

**Who benefits**: Users who change `adjustmentTickRate` or the gains learn about an oscillating config when it is
loaded, not from the fan noise.

**Trade-offs**: The model ignores the clamping to `[0..255]` and the anti-windup, which only matter once the
output hits a limit, and assumes that a cycle takes exactly `adjustmentTickRate`. A fan whose every update takes
longer than the gap threshold (e.g. a slow `cmd` fan) runs the loop at `max(1s, adjustmentTickRate)` instead (see
"The PID algorithm doesn't integrate over a gap"). Validation therefore also checks that step
(`control_loop.GapDt`), but only warns if the values are unstable there, because it only affects slow fans: for
example, `p: 0.05, i: 1.25` is stable at `200ms` and unstable at `1s`. Configs that worked "well enough" with an oscillating fan are now rejected. The overshoot threshold of 1 % is a
judgement call (about 2.5 PWM for a full-range step).

**Alternatives considered**: A rule of thumb like `i * adjustmentTickRate < 1`. It ignores `p` and `d`, and misses
the instability for small tick rates. Filling omitted gains with the defaults instead of rejecting `i: 0`: that
would silently change working configs that rely on an omitted `p` or `d` being `0`.

### The PID algorithm starts at the curve value (#552)

**Decision**: The first `Cycle` returns the curve value directly (clamped to `[0..255]`) and presets the PID loop as
if it had settled there: `PidLoop.Preset(output, measured)` sets `integral = output / i`, so the I term alone yields
the output (bumpless transfer).

**Why**: Before #552 the loop started from 0. The first output was `p * target` (10 for a target of 200 with the
default values). On every start and config reload the fan dropped to near its minimum and ramped up over 5-10 s.

**Who benefits**: Everyone on the `pid` algorithm. Fans no longer dip on start and on config reloads. This also
matters for systems that reload the config often.

**Alternatives considered**: Users who want a slow start can use `direct` with `rampUp` (#552 named
`maxPwmChangePerCycle`, which is deprecated now).

**Trade-offs**: With `i: 0` the preset couldn't hold the output, because only the I term holds a value at steady
state. Validation rejects `i <= 0` (see "Validation computes the stability of the PID algorithm").

### Smoothing is a set of options of `direct`, based on time

**Decision**: `direct` has optional stages, all off unless configured: a first-order low-pass filter
(`timeConstant`), a deadband on the output (`deadband`) and ramp rates per second (`rampUp`, `rampDown`). They use
the time between two cycles (`now` from the fan controller's clock), not the number of cycles. There is no separate
smoothing algorithm.

**Why**: the `pid` algorithm and `maxPwmChangePerCycle` depend on `adjustmentTickRate` (see above and below). Users
raise the tick rate to save CPU wakeups and then get overshoot, or ramps that are five times slower. Other fan
control tools model smoothing as modifiers applied to the curve value: a rate limit per second, often separate for
up and down, and sometimes a minimum step. `direct` with all stages off is the plain pass-through, so one algorithm
with options avoids two names for the same thing.

- The low-pass is discretized exactly (`1 - exp(-dt/τ)`), so the output after a given time is the same at every
  tick rate and for irregular cycles (coalesced evaluator notifications, slow fans). It can't overshoot.
- The deadband is what cuts the number of writes at a steady, noisy temperature: in a simulation (curve
  `40..80 °C`, temperature noise `σ = 0.5 °C`, `200ms` ticks), the low-pass alone writes about as often as `pid`
  (≈10 per minute), with a deadband of 1 to 2 units ≈3 to 0.4 per minute.
- The ramps give "fast up, slow down", the most common wish in other tools. They are per second.

**Who benefits**: everyone on the default algorithm (no tick-rate dependence), users who want a slower decrease,
users of fans without PWM readback (every output change is a write).

**Trade-offs**:

- The deadband leaves a steady offset of less than `deadband` from the curve value (with the default 1 %, below
  what can be heard). Fan off (`0`), full speed (`255`), and starting or stopping a fan are never held back.
  With `useUnscaledCurveValues`, the fan stops below its `minPwm` instead of below `1`, which the control
  algorithm doesn't know: there, a start or stop can be delayed until the change reaches the deadband.
  CoolerControl also releases a held value after 30 s; fan2go doesn't, because the offset is bounded and the
  exceptions cover the cases where it matters.
- The deadband compares the filtered value with the output, not the ramp step, so a ramp that moves less than the
  deadband per cycle still moves. While the filtered value drifts slowly (the end of a low-pass), the output moves
  in steps of about `deadband`.
- All values are in curve units (`[0..255]`), before the scaling to `minPwm`..`maxPwm`. A deadband of 1 % is less
  than 1 % of the fan's PWM range if that range is narrower. With `useUnscaledCurveValues`, curve units are PWM values
  before the `pwmMap`.
- The output never moves away from the target. Without this rule, a slow ramp and a low-pass that lags behind a
  target that reversed would keep the fan decreasing for a moment after the curve went up again.

### Percentages of rates and deadbands are linear

**Decision**: `rampUp`, `rampDown` and `deadband` take a plain number (curve units, per second for the ramps) or a
percentage of the curve value range: `"N%"` is `N x 2.55` (`configuration.CurveUnits`).

**Why**: curve steps map `1%` to `1` (the minimum speed) and then interpolate to `100%` = `255`. That offset is
right for an absolute speed, but wrong for a difference or a rate: `10%` per second must be a tenth of the range.

### `direct` uses the real time between two cycles, without a gap rule

**Decision**: the time-based stages of `direct` use the real time since the last cycle as `dt`, however long it is.
A cycle before the last one (the clock went backwards) counts as no time.

**Why**: unlike `pid`, a low-pass or a rate limit can't saturate or oscillate with a large `dt`: after a long pause it
moves at most to the target. The pauses in which the fan was set without the loop are handled before: after a curve
failure the loop is preset or reset (below), and `Run` resets it on every (re)start. A fan whose every update takes
long (for example a slow `cmd` fan) is smoothed exactly as configured.

**Trade-offs**: after a pause without a preset or reset (for example a write that blocked for a long time), the
output can move by the whole pause's worth at once.

### `maxPwmChangePerCycle` counts cycles (deprecated)

**Decision**: `maxPwmChangePerCycle` limits the change per call of `Cycle`, in curve value units. It still works as
before, but it is deprecated: the config validation logs a warning with the equivalent per second at the configured
`adjustmentTickRate` (`maxPwmChangePerCycle / adjustmentTickRate`), and rejects it together with `rampUp` or
`rampDown`. It can be combined with `timeConstant` and `deadband`; it replaces the ramps then.

**Why**: It was originally named `PwmChangePerSecond` (#267) and renamed to `maxPwmChangePerCycle` in the config
rework, because the limit applies per cycle. `rampUp`/`rampDown` replace it with a per-second rate. It is kept so
existing configs keep working.

**Trade-offs**:

- The ramp speed is `maxPwmChangePerCycle / adjustmentTickRate`. With `maxPwmChangePerCycle: 10` and the default
  `200ms`, that is 50 per second, so a full `0` to `255` change takes about 5 s. With `1s` it takes about 26 s.
- If a fan's updates are slow (for example a slow `cmd` fan, or a long `pwmSetDelay`), its controller skips
  evaluator cycles, and its ramp slows down too.

### Both algorithms clamp to [0..255] and store the clamped value (#552)

**Decision**: The first cycle of both algorithms is clamped like every other cycle. The direct algorithm stores the
clamped value as the base of the next ramp step.

**Why**: Before #552 the direct algorithm stored an out-of-range first target (for example 300). A later drop of
the target then started the ramp from 300 instead of 255, which delayed the fan's reaction.

**Who benefits**: Safety. The built-in curve types all return values in `[0..255]` (step values are validated,
`sum` and `difference` function curves are capped, the PID curve clamps its output). The clamp is a defensive
bound: it keeps the loop state equal to what can be applied if a target is ever out of range. `UpdateFanSpeed`
logs a warning for out-of-range values for the same reason.

### The control loop is not cycled while the curve fails

**Decision**: `calculateTargetSpeed` returns the curve error before it calls `Cycle`. While the curve can't be
evaluated, the `onSensorFailure` action applies (`max` or `restore`, #551) and the control loop keeps its old state.
How a stale sensor becomes a curve error is described in [curves.md](curves.md) and [sensors.md](sensors.md).

**Why**: There is no valid target to feed into the loop. Not cycling keeps the loop state from before the failure.

**Trade-offs**: The loop state from before the failure no longer matches the fan after it. The next two decisions
handle that.

### The control loop continues from the fan's speed after a curve failure

**Decision**: When the curve can be evaluated again while an `onSensorFailure` action is active,
`calculateTargetSpeed` adjusts the loop before it cycles it (this runs before `recoverFromCurveFailure`):

- `max` (also the `restore` fallback to max): `Preset` at `255`, the curve value that maps to `maxPwm`, or the
  fail-safe speed itself (the configured `maxPwm`, else 255; nvidia: 100) with `useUnscaledCurveValues`. The `pid` algorithm and `direct` with a time constant or ramps return that
  value on the first cycle and smooth down from there (the default algorithm comes down with its time constant of
  2 s). `direct` with `maxPwmChangePerCycle` ramps down from it; without options it applies the target.
- `restore`: `Reset`. fan2go doesn't know the speed the fan ran at while it was handed back, so the loop starts at
  the target like on startup.

**Why**: The fail-safe max write bypasses the control loop (it must not be slowed by it), so the loop doesn't know
the fan's speed. Before, the output continued from the value before the failure: with `direct` and
`maxPwmChangePerCycle` the fan jumped from `maxPwm` to near the old value, and `pid` integrated over the whole
failure (see "The PID algorithm doesn't integrate over a gap").

**Who benefits**: Users with `onSensorFailure` whose sensors recover: no audible drop or jump of the fan.

**Trade-offs**: With `restore`, if manual control can't be taken back yet, the loop is cycled anyway and its output
isn't applied. It is reset again on every cycle until manual control is taken back, so this is harmless. If the
max write failed during the failure, the loop still starts at max, which errs on the side of cooling.

### The PID algorithm doesn't integrate over a gap

**Decision**: If more than `max(2s, 5 x adjustmentTickRate)` passed since the last cycle, `PidControlLoop` counts the
gap as `max(1s, adjustmentTickRate)`: it uses that as `dt` of the cycle (`PidLoop.ResumeAt`) and keeps its state.
Both are computed from the tick rate passed to `NewPidControlLoop` (`maxCycleGap`, `GapDt`). `direct` has no gap
rule (see above).

**Why**: `PidLoop.LoopAt` integrates `err * dt` with no upper bound on `dt`. After a pause (a curve failure, a
stalled update loop) the I term saturated at once: with the default values, output 100 before the gap and curve
value 50 after a gap of 5 s or more gave `97.5`, then `0.0`, then a ramp back over about 10 s. Output 0 means PWM 0
for a fan without `neverStop`, so the fan stopped briefly. This is defence in depth next to the preset above: it
also covers gaps without a failure. System suspend is not a gap: Go measures `dt` with a monotonic clock, which
doesn't advance during suspend on Linux.

Counting the gap as a short, fixed time, instead of holding the last output, keeps the loop moving towards the
target when every cycle takes longer than the threshold. That happens if a single update of the fan takes more than
about 2 s (e.g. a slow `cmd` fan with a PWM readback). Holding the output would freeze such a fan at its first
speed, even if the curve went to 255.

Why 1 s: simulated with the default values and updates every 3 s, a step reaches 90% after:

| A gap counts as | Updates to 90% (step up / down) | Lowest output for 100 to 50 |
|---|---|---|
| one tick (`200ms`) | 28 / 28 | 50, no overshoot |
| `1s` | 4 / 4 | 46.1 (8% overshoot) |
| `2s` (the threshold at `200ms`) | 3 / 12 | 17.5 |
| `3s` (the whole update) | 2 / never settles | 0.0, oscillates |

1 s is well below the cycle time at which the default values start to oscillate (about 2.64 s). With a tick of
1 s or more, a gap counts as one tick. A slow fan thus runs the loop at a step of `max(1s, adjustmentTickRate)`, so
the `pid` values must also be stable at that step, not only at `adjustmentTickRate`. Validation warns if they aren't
(see "Validation computes the stability of the PID algorithm").

**Who benefits**: Everyone on the `pid` algorithm. Normal operation is unchanged: evaluator cycles that are
coalesced or slightly delayed stay far below the threshold as long as one update of the fan takes well under 2 s.

**Trade-offs**:

- A gap just below the threshold is still integrated (at `200ms`, a 2 s gap gives `97.5`, then `57.8` for the
  example above), which is a much smaller effect.
- A fan whose every update takes longer than the threshold moves 1 s worth per update: it gets within 10% of a new
  curve value after about 4 updates, and may overshoot a step down by about 8%.
- The 1 s fits the default values. With a larger `i`, a slow fan can overshoot more, or oscillate between `0` and
  `255` without end if the values are unstable at 1 s (e.g. `p: 0.05, i: 1.25`); validation warns about the latter.

### `direct` with a time constant and a deadband is the default (1.0.0)

**Decision**: A fan without `controlAlgorithm` (and without the deprecated `controlLoop`) uses `direct` with
`timeConstant: 2s` and `deadband: 1%` (2.55), no ramps (`DefaultControlAlgorithmConfig`). An explicit
`controlAlgorithm: direct` (string form, or `direct:` without options) is the pure pass-through, as before.
`controlAlgorithm: pid` (string form) uses the default PID values.

**Why**: Before 1.0.0 the default was `pid`, kept since #267 for backwards compatibility. It is a filter with a time
constant of about 2.3 s at `200ms`, but it overshoots from a tick rate of about `700ms` and is unstable from about
`2.64s`. A low-pass of 2 s feels the same at the default tick rate and doesn't depend on it. The deadband reduces the
writes at a steady temperature. 1.0.0 is a breaking release anyway, so the default changed there. Giving the
options of `direct` defaults instead would have changed explicit `direct` configs too, whose users chose "no
smoothing".

**Who benefits**: Everyone who doesn't configure a control algorithm: the same feel at `200ms`, no overshoot at
slower tick rates, fewer writes.

**Trade-offs**:

- Breaking for configs without `controlAlgorithm`: the response is a first-order filter instead of PID's
  second-order one (it settles a step of 150 to within 2 units in about 9 s, before about 10 s at `200ms`), and the
  output stays within 1 % of the curve value instead of reaching it exactly. `controlAlgorithm: pid` restores the
  old behaviour.
- The string form `direct` and the omitted case behave differently. The README and the example config say so.
- No per-hardware defaults.

## Configuration

Per fan:

```yaml
fans:
  - id: some_fan
    # omitted: direct with timeConstant: 2s, deadband: 1%

  - id: other_fan
    # string form: pass-through (direct) or the default values (pid)
    controlAlgorithm: direct      # or: pid

  - id: third_fan
    controlAlgorithm:
      direct:
        timeConstant: 2s          # optional, low-pass on the output
        rampUp: 10%               # optional, max increase per second
        rampDown: 2%              # optional, max decrease per second
        deadband: 1%              # optional, smallest change that is applied

  - id: fourth_fan
    controlAlgorithm:
      pid:
        p: 0.05
        i: 0.4
        d: 0.01
```

| Key | Default | Notes |
|---|---|---|
| `controlAlgorithm` | `direct` with `timeConstant: 2s`, `deadband: 1%` | String (`pid`, `direct`) or object. The string `direct` has no options. |
| `controlAlgorithm.direct.timeConstant` | `0` (off) | Duration (`2s`, `500ms`; a plain number is seconds, like `fanResponseDelay`). Must be `>= 0`; warning below `100ms` (except `0`) and above `1m`. |
| `controlAlgorithm.direct.rampUp`, `.rampDown` | `0` (no limit) | Curve units per second, or `"N%"` (= `N x 2.55`). Must be `>= 0` and finite. Warning if a full increase takes more than 60 s. |
| `controlAlgorithm.direct.deadband` | `0` (off) | Curve units or `"N%"`. Must be `>= 0`, finite and `< 255`; warning above 10 %. |
| `controlAlgorithm.direct.maxPwmChangePerCycle` | not set (no limit) | Deprecated (warning with the per-second equivalent). Must be `> 0`. In curve value units per cycle. Can't be combined with `rampUp`/`rampDown`. |
| `controlAlgorithm.pid.p`, `.i`, `.d` | see above; in the object form, omitted keys are `0` | `i` must be `> 0`. Unstable combinations with `adjustmentTickRate` are rejected, overshoot causes a warning. |
| `controlLoop.p`, `.i`, `.d` | - | Deprecated. Creates a `pid` algorithm, validation logs a warning. Validated like `controlAlgorithm.pid`. |
| `fanController.adjustmentTickRate` (global) | `200ms` | Sets the cycle rate. Changes the ramp speed of `maxPwmChangePerCycle` and the response of `pid`, not the time-based options of `direct`. |

Validation rejects a `controlAlgorithm` with both `direct` and `pid`, an empty one (e.g. `direct:` without a value,
which would otherwise get the default instead of the pass-through), and `controlLoop` together with
`controlAlgorithm`.

## Known limitations and open questions

- **No feedback on the fan.** Neither algorithm corrects for a fan that doesn't reach the expected speed. The
  `neverStop` stall check in the fan controller is the only RPM-based correction.
- **Values in the curve value space.** Both algorithms run before the scaling to `minPwm`..`maxPwm`. The meaning of
  the ramps, the deadband and `maxPwmChangePerCycle` in actual PWM depends on the fan's range.
- **No per-hardware smoothing defaults.** Every fan gets the same default, whatever its type.
