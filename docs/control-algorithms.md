# Control algorithms

A control algorithm decides how a fan moves from its current PWM value to the value its curve asks for. fan2go
offers two: `direct` (apply the curve value, optionally rate-limited) and `pid` (smooth the curve value with a PID
loop). `pid` is the default. The algorithms live in `internal/control_loop` (`DirectControlLoop`,
`PidControlLoop`), the PID math in `internal/util/pid.go` (`PidLoop`), and the selection in `internal/factory.go`
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
        |
        v  controlLoop.Cycle(curveValue)        [0..255] in, [0..255] out
DefaultFanController.UpdateFanSpeed
        |  scale [1..255] to [minPwm..maxPwm], neverStop, stall check
        v
setTargetPwm --> pwmMap --> fan.SetPwm
```

The control algorithm works in the curve value space `[0..255]`. It runs before the value is scaled to the fan's
`minPwm`..`maxPwm` range and before `neverStop` and the `pwmMap` are applied. It never sees the fan's RPM or the PWM
value read back from the hardware.

The interface is a single method (`internal/control_loop/common.go`):

```go
type ControlLoop interface {
	Cycle(target float64) float64
}
```

Each fan controller owns one `ControlLoop` instance. A new instance is created on every start and every successful
config reload, so all algorithm state starts over then. If a reload fails after the old controllers were stopped
(an error while creating the new objects; an invalid config doesn't stop them), fan2go restarts the old fan
controllers with their old `ControlLoop` instances, and `Run` resets them first (`ControlLoop.Reset`), so the state
starts over as well.

### Direct

`DirectControlLoop.Cycle`:

- First cycle: return the target, clamped to `[0..255]`.
- Later cycles without `maxPwmChangePerCycle`: return the target, clamped to `[0..255]`.
- Later cycles with `maxPwmChangePerCycle`: move from the last output towards the target by at most
  `maxPwmChangePerCycle`, then clamp to `[0..255]`. The clamped value is stored as the base of the next step.

The direct algorithm doesn't look at the time. It counts cycles.

### PID

`PidControlLoop.Cycle`:

- First cycle: return the target, clamped to `[0..255]`, and call `PidLoop.Preset(target, target)`.
- Later cycles: `PidLoop.Loop(target, measured)` with `measured = lastPidOutput`, the previous output of this
  same loop. The result (clamped to `[0..255]` by `PidLoop`) becomes the new `lastPidOutput`.

`PidLoop.LoopAt` computes, with `dt` the time since the last call (from `time.Now`, so a monotonic clock):

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

**Who benefits**: Everyone with the default config: noisy or spiky sensors don't make the fans audibly hunt.
Users who want a real temperature feedback loop use a `pid` curve instead (see [curves.md](curves.md)). That curve
measures a sensor, has a setpoint, and is unrelated to this control algorithm. With a `pid` curve and the default
`pid` control algorithm, the filter adds its lag behind the curve's feedback loop.

**Alternatives considered**:

- Linear ramp (`direct` with `maxPwmChangePerCycle`). It was added in #267 (issue #266) as a replacement for the
  PID approach, which at that time didn't reach its targets. Both were kept, and the user picks one per fan.
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
| about `2.7s` and slower | Unstable. The output oscillates between the limits and never settles. |

The reason is the discrete update: the I term changes the output by `i * dt * err` per cycle. Once `i * dt`
approaches 1, one cycle overcorrects. The D term acts as `d / dt` on the change of the loop's own output, so a large
`d` also causes overshoot (`d: 0.5` reaches 255 for a step to 200 at `200ms`).

**Who benefits**: Users on the default `200ms` cycle get a smooth, overshoot-free response.

**Trade-offs**: Users who raise `adjustmentTickRate` (for example, to reduce CPU wakeups) change the behaviour of
every fan that uses the `pid` algorithm. The README says to lower `i` accordingly. Nothing in the config validation
warns about an unstable combination.

### The PID algorithm starts at the curve value (#552)

**Decision**: The first `Cycle` returns the curve value directly (clamped to `[0..255]`) and presets the PID loop as
if it had settled there: `PidLoop.Preset(output, measured)` sets `integral = output / i`, so the I term alone yields
the output (bumpless transfer).

**Why**: Before #552 the loop started from 0. The first output was `p * target` (10 for a target of 200 with the
default values). On every start and config reload the fan dropped to near its minimum and ramped up over 5-10 s.

**Who benefits**: Everyone on the default algorithm. Fans no longer dip on start and on config reloads. This also
matters for systems that reload the config often.

**Alternatives considered**: Users who want a slow start can use `direct` with `maxPwmChangePerCycle` (named in the
#552 upgrade notes).

**Trade-offs**: With `i: 0` the preset can't hold the output, because only the I term holds a value at steady state.
See "Known limitations".

### The direct algorithm counts cycles, not time

**Decision**: `maxPwmChangePerCycle` limits the change per call of `Cycle`, in curve value units.

**Why**: It was originally named `PwmChangePerSecond` (#267) and renamed to `maxPwmChangePerCycle` in the config
rework, because the limit applies per cycle. A per-cycle limit is simple and deterministic in tests.

**Who benefits**: Users who want a predictable linear ramp, for example to avoid audible speed jumps.

**Trade-offs**:

- The ramp speed is `maxPwmChangePerCycle / adjustmentTickRate`. With `maxPwmChangePerCycle: 10` and the default
  `200ms`, that is 50 per second, so a full `0` to `255` change takes about 5 s. With `1s` it takes about 26 s.
  The README notes that `adjustmentTickRate` sets the time unit.
- The limit is in curve value units. After scaling to `minPwm`..`maxPwm` the actual PWM step is smaller when the
  fan's range is narrower than `[0..255]`.
- If a fan's updates are slow (for example a slow `cmd` fan, or a long `pwmSetDelay`), its controller skips
  evaluator cycles, and its ramp slows down too. Slow sensors don't affect the cycle rate, because curves only read
  the values the sensor monitors recorded (see [sensors.md](sensors.md)).

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

**Trade-offs**:

- The `pid` algorithm measures the time since its last call. After a failure state, that whole gap becomes `dt` of
  the first cycle. See "Known limitations".
- Neither algorithm knows that the fan was at `maxPwm` (or handed back) during the failure. After the recovery the
  output continues from the value before the failure, not from the fan's actual speed. With `direct` and
  `maxPwmChangePerCycle`, the fan doesn't ramp down from `maxPwm`; it jumps to near the old value.
- With `restore`, the loop is cycled again as soon as the curve recovers, even while fan2go can't take manual
  control back yet. Its output isn't applied then, but its state moves on.

### PID is the default

**Decision**: A fan without `controlAlgorithm` uses `pid` with the default values (`createControlLoop`, fallback).
`controlAlgorithm: pid` (string form) also uses the default values; `controlAlgorithm: direct` uses no limit.

**Why**: PID was the original (and for a long time only) control logic. When `direct` was added in #267, PID stayed
the default for backwards compatibility (stated in #267).

**Who benefits**: Existing users keep their behaviour. New users get smoothing without configuring anything.

**Trade-offs**: The default depends on the cycle rate (see above). Users who expect "the fan follows the curve"
see a delay of several seconds.

## Configuration

Per fan:

```yaml
fans:
  - id: some_fan
    # string form: default values of the chosen algorithm
    controlAlgorithm: direct      # or: pid

  - id: other_fan
    controlAlgorithm:
      direct:
        maxPwmChangePerCycle: 10  # optional, must be > 0, no default (no limit)

  - id: third_fan
    controlAlgorithm:
      pid:
        p: 0.05
        i: 0.4
        d: 0.01
```

| Key | Default | Notes |
|---|---|---|
| `controlAlgorithm` | `pid` with `p: 0.05`, `i: 0.4`, `d: 0.01` | String (`pid`, `direct`) or object. |
| `controlAlgorithm.direct.maxPwmChangePerCycle` | not set (no limit) | Validation rejects values `<= 0`. In curve value units per cycle. |
| `controlAlgorithm.pid.p`, `.i`, `.d` | see above; in the object form, omitted keys are `0` | Validation rejects only the case where all three are `0`. |
| `controlLoop.p`, `.i`, `.d` | - | Deprecated. Creates a `pid` algorithm and logs a warning. Not validated. |
| `fanController.adjustmentTickRate` (global) | `200ms` | Sets the cycle rate. Changes the ramp speed of `direct` and the response of `pid`. |

If both `direct` and `pid` are set in the object form, `pid` wins silently. The deprecated `controlLoop` takes
precedence over `controlAlgorithm`.

## Known limitations and open questions

- **Output jump after a pause in cycling.** After a failure state (`onSensorFailure` was active) the first `pid`
  cycle sees the whole gap as `dt`. If the curve value changed during the gap, the integral term saturates at once
  and the output jumps to `0` or `255`, then recovers over about 10 s. Example with the default values, output 100
  before the failure, curve value 50 after a gap of 5 s or more: outputs `97.5`, then `0.0`, then `7.4`, then a
  ramp back to about 50 over about 10 s. For a curve value of 200: `105`, then `255`, then a ramp down. With a 2 s
  gap the effect is smaller (`97.5`, then `57.8`). There is no jump if the curve value is unchanged after the gap.
  Unlike the `pid` curve, which resets its loop after a stale period
  (see [curves.md](curves.md)), the control algorithm has no such reset. System suspend doesn't trigger this on
  Linux, because Go measures `dt` with a monotonic clock that stops during suspend.
- **No stability check for the cycle rate.** With the default values, `adjustmentTickRate` of about `2.7s` or
  slower makes the `pid` algorithm oscillate forever. Overshoot already starts below `1s`.
- **`i: 0` collapses the output.** The steady-state output of the `pid` algorithm comes from the I term. With `i: 0`
  and `p: 0.05`, a constant curve value of 200 gives `200` on the first cycle, `0` on the second and then settles at
  about `9.5` (`p / (1 + p)` of the curve value; with `d: 0.01` after a short swing: 20, 8, 10.2, ...). Validation
  accepts this config. The deprecated `controlLoop` with all values `0` gives `0` after the first cycle.
- **No feedback on the fan.** Neither algorithm corrects for a fan that doesn't reach the expected speed. The
  `neverStop` stall check in the fan controller is the only RPM-based correction.
- **The PID clock isn't the controller's clock.** `PidControlLoop` uses `time.Now` unless created with
  `NewPidControlLoopWithClock`. The fan controller's injectable clock (#542) doesn't drive it, so controller tests with
  a fake clock and the `pid` algorithm use real time for `dt`.
- **Values in the curve value space.** Both algorithms run before the scaling to `minPwm`..`maxPwm`. The meaning of
  `maxPwmChangePerCycle` in actual PWM depends on the fan's range.
- Open question: whether the filter should be expressed in time (for example, a time constant in seconds) instead of
  PID values, so that it doesn't depend on `adjustmentTickRate`.
