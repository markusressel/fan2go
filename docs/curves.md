# Speed curves

A speed curve turns one or more sensor values into a target speed for the fans that use it. fan2go has four curve
types (`linear`, `staircase`, `pid` and `function`). They all produce a value in `0..255`. A central curve evaluator
evaluates every curve once per cycle, and the fan controllers read the stored results. The code lives in
`internal/curves` (curve types and `CurveEvaluator`), `internal/configuration/curves.go` (config types),
`internal/configuration/config.go` (step parsing), `internal/configuration/validation.go` (validation) and
`internal/factory.go` / `internal/daemon.go` (wiring and lifecycle).

This document covers the curves only. What a fan controller does with a curve value (direct or PID smoothing,
`maxPwmChangePerCycle`) is described in [control-algorithms.md](control-algorithms.md). How sensors are polled and
averaged is described in [sensors.md](sensors.md).

## How it works

```
 sensor monitors (one goroutine per sensor, every tempSensorPollingRate)
   | record last raw reading + moving average
   v
 sensors.Sensor  <-- read only, never polled by curves
   |
   | CurveEvaluator.RunCycle(), every fanController.adjustmentTickRate
   |   for each curve in dependency order:
   |     linear / staircase: staleness check, then moving average
   |     pid:                staleness check, then last raw reading (steps only on a new reading)
   |     function:           CurrentResult() of its sub-curves
   |   store CurveResult{Value, Err, EvaluatedAt} in the curve
   |   notify subscribers
   v
 fan controllers (one per fan): read curve.CurrentResult()
   Err == nil -> control algorithm -> PWM mapping -> fan
   Err != nil -> onSensorFailure (max | restore)
 REST API (/curve), Prometheus collector: read the stored value
```

Main types:

- `SpeedCurve` (`internal/curves/curve.go`): `GetId`, `Evaluate`, `CurrentValue`, `CurrentResult`.
- `CurveResult`: `Value` (in `0..255`), `Err` (nil on success), `EvaluatedAt` (zero before the first evaluation).
  Before the first evaluation, `Err` is `ErrCurveNotEvaluated`.
- `LinearSpeedCurve`, `StaircaseSpeedCurve`, `PidSpeedCurve`, `FunctionSpeedCurve`: one type per curve type.
  `NewSpeedCurve` picks the type from the config block that is set.
- `CurveEvaluator` (`internal/curves/evaluator.go`): evaluates all curves once per cycle and notifies subscribers.
- `RegistryReader`: the curves look up sensors and sub-curves by id through the registry. `Registry.RegisterCurve`
  binds the registry to each curve (`BindRegistry`).

Timing: the evaluator ticks at `fanController.adjustmentTickRate` (default `200ms`). It runs one cycle right away
when it starts. After each cycle it sends a notification to every subscriber. Each fan controller subscribes
(`SetCurveCycles`) and updates its fan once per notification.

## Curve types

### Value range and units

- Curve values are `float64` in `0..255`. `0` means "fan off" (unless `neverStop`), `255` means full speed. The fan
  controller maps `1..255` to the fan's `minPwm..maxPwm` (unless `useUnscaledCurveValues: true`). See
  [fan-controller.md](fan-controller.md#from-curve-value-to-pwm-value) for the mapping.
- Step values in `linear` and `staircase` curves are strings in the config: a plain number (`0..255`) or a percent
  value (`0%..100%`). `transformCurveSteps` converts them when the config is loaded: values below `1%` become `0`,
  `1%..100%` map linearly to `1..255` (`(p-1)*254/99+1`). Values outside the range, `NaN` and `Inf` are rejected.
- Temperatures in the config (`min`, `max`, step keys, `setPoint`) are in degrees. Sensor values are in
  millidegrees, so the curves divide sensor values by 1000. Step keys and `min`/`max` are integers, `setPoint` is a
  float.

### Linear

`LinearSpeedCurve` uses the sensor's moving average.

- With `steps`: piecewise linear interpolation between the steps (`util.CalculateInterpolatedCurveValue`). Below the
  lowest step the curve returns the lowest step's value, above the highest step the highest step's value.
- Without `steps`: `min` maps to `0`, `max` maps to `255`, linear in between. At or above `max` the value is `255`,
  at or below `min` it is `0`.
- If both `steps` and `min`/`max` are set, `steps` wins, and validation warns that `min`/`max` are ignored.
- Without `steps`, validation requires `min < max`. A curve with neither (both `0`) used to return `255` for every
  temperature at or above 0 °C.

### Staircase

`StaircaseSpeedCurve` (#486) uses the sensor's moving average and does not interpolate. It picks the highest step
whose temperature is at or below the average and returns that step's value. Below the lowest step it returns `0`.
It keeps the temperature of the selected step in `LastTemp`. The REST API shows it as `lastTemp`, and omits it
before the first evaluation and while the temperature is below the lowest step (internally `math.MinInt`).

`hysteresis.down` delays a step down: the curve only moves to a lower step once the temperature has fallen
`down` degrees below the temperature of the current step (`LastTemp`). Moving up has no hysteresis.

### PID

`PidSpeedCurve` runs a PID loop (`util.PidLoop`) with output range `0..255`, anti-windup on, anti-winddown off. The
integral is clamped so the I term stays within the output range (#529). The error is `setPoint - temperature`, so a
cooling curve needs negative gains (the README example uses `p: -0.05`).

The PID curve is not the same as `controlAlgorithm: pid` of a fan, although both use `util.PidLoop`. The PID curve is
a feedback loop on a sensor with a `setPoint`. The `pid` control algorithm feeds its own previous output back and
only smooths the curve value (see [control-algorithms.md](control-algorithms.md)).

Unlike the other types, it uses the sensor's last raw reading, not the moving average. It only steps when a new
reading has arrived since its last step, and uses the time between the two readings as `dt` (`PidLoop.LoopAt`).
The first step returns the P term only.

### Function

`FunctionSpeedCurve` combines the results of two or more other curves (`curves:`) with one of these functions:

| `type`       | Result                                                    |
|--------------|-----------------------------------------------------------|
| `minimum`    | smallest value                                            |
| `maximum`    | largest value                                             |
| `average`    | arithmetic mean                                           |
| `delta`      | largest minus smallest value                              |
| `sum`        | sum, capped at `255` (#194)                               |
| `difference` | first value minus all others, floored at `0` (#194)       |

A function curve doesn't evaluate its sub-curves. It reads their stored results from the current cycle.
Function curves can reference other function curves.

## Design decisions

### One central evaluator, every curve once per cycle

**Decision**: a single `CurveEvaluator` goroutine evaluates every configured curve exactly once per cycle, in
dependency order, including curves no fan uses. Fan controllers, the REST API and the Prometheus collector only read
the stored result (`CurrentResult`, `CurrentValue`, `MarshalJSON`). In the daemon, nothing else calls `Evaluate`.

**Why**: before #553, each fan controller evaluated its own curve from its own goroutine and ticker. A curve shared
between fans (directly or through a `function` curve) was evaluated several times per cycle, concurrently. This was a
data race on curve state (the staircase curve's `LastTemp`, reached through a `function` curve), it let fans that
share a curve act on values from different moments, and it advanced a shared PID curve more often than intended.
Curves that no fan used had no live value in the API.

**Who benefits**: users who share curves between fans (all of them now see the same value in the same cycle),
users of `pid` curves shared between fans (the loop advances once per cycle, not once per fan), people who monitor
fan2go through the REST API or Prometheus (live values for all curves), and maintainers (one writer per curve, no
data race).

**Alternatives considered**: an event-driven sensor -> curve -> fan pipeline and per-fan tick rates were listed as
out of scope in #553. Keeping per-controller evaluation with more locking would not fix the "different moments"
problem or the repeated PID steps.

**Trade-offs**:

- Unused curves cost CPU every cycle (small).
- All fans share one rate, `fanController.adjustmentTickRate`. There is no per-fan or per-curve rate.
- A fan's update can shift by up to one cycle compared to before #553.

### Dependency order, checked twice

**Decision**: `evaluationOrder` sorts the curves topologically, so the sub-curves of a `function` curve come first.
Curves without a dependency between them are sorted by id, so the order is deterministic. An unknown reference, a
duplicate id or a cycle makes `NewCurveEvaluator` fail.

**Why**: a function curve reads the results of its sub-curves from the same cycle, so they must be evaluated first.
Config validation (`validateCurves`) already rejects self-references, unknown ids and cycles (Tarjan's algorithm).
The evaluator's check is a safety net, so a bug elsewhere ends in an error instead of an endless recursion.

**Who benefits**: users with nested `function` curves (results are never one cycle behind), maintainers
(deterministic order makes tests reproducible).

**Trade-offs**: the cycle check exists in two places with different algorithms.

### Notification instead of a second ticker

**Decision**: after each cycle the evaluator sends to one buffered channel (size 1) per subscriber. If the
subscriber hasn't received the previous notification yet, the new one is dropped. A fan controller with a
subscription has no ticker of its own. At startup, it waits for the first cycle if its curve hasn't been evaluated
yet (`ErrCurveNotEvaluated`), instead of treating that as a failure.

**Why**: a fan controller should act right after the curves have been evaluated, on the values of that cycle. A slow
controller (e.g. one that waits for a slow PWM write) must not block the evaluator or the other fans. Without the
startup wait, a controller that started before the first cycle would set its fan to `maxPwm` and notify a sensor
failure (#553).

**Who benefits**: everyone: all fans see the same cycle, and one stuck fan doesn't delay the others.

**Trade-offs**: a slow controller skips cycles instead of catching up. That is intended: it always acts on the
latest result. The control algorithm still runs once per update, so `maxPwmChangePerCycle` counts updates, not
evaluator cycles.

### Curves never read sensors

**Decision**: curves don't call `GetValue` on sensors. They use what the sensor monitors recorded: the moving
average and the last successful raw reading with its time (`getSensorReading`, `sensors.FreshReading`). See
[sensors.md](sensors.md).

**Why**: before #551 the PID curve read the sensor synchronously. One failed read (a cmd sensor timeout, an NVML
error) stopped the fan controller for good. Reading hardware from the evaluator would also let one slow sensor (e.g.
a `cmd` sensor) delay every curve and every fan.

**Who benefits**: users with slow or flaky sensors (cmd, nvidia, disk). A cycle never waits for hardware.

**Trade-offs**: a curve value can be up to one `tempSensorPollingRate` old, plus up to one cycle.

### Moving average for linear and staircase, raw reading for PID

**Decision**: `linear` and `staircase` curves use the sensor's moving average. `pid` curves use the last raw reading,
and step only when a new reading has arrived, with the time between readings as `dt`.

**Why**: the moving average smooths noise, which keeps fans from reacting to every spike. A PID loop has its own
dynamics: smoothing its input adds lag that the gains would have to compensate, and the README describes the PID
curve as working on the raw signal, so existing tunings stay valid (#551). Stepping only on new readings means
evaluating the curve more often than the sensor is polled doesn't distort the I and D terms. Using the reading time
as `dt` keeps the I and D terms correct when cycles and readings don't line up.

**Who benefits**: users who tune PID curves: the result depends on the sensor data, not on how often fan2go
evaluates the curve or how many fans use it.

**Trade-offs**:

- If the sensor is polled faster than the evaluator ticks, the PID curve skips the readings in between. It always
  uses the latest one.
- The loop steps at most once per cycle, so its effective sample interval is about
  `max(tempSensorPollingRate, adjustmentTickRate)`. Like any discrete PID loop, its response (overshoot, stability)
  depends on that interval: changing either rate can require retuning the gains.
- `tempRollingWindowSize` has no effect on PID curves. This is easy to miss.
- A fan that uses a `pid` curve with the default `pid` control algorithm gets a smoothing filter (time constant of a
  few seconds) behind the PID loop, which adds lag to the feedback loop. `controlAlgorithm: direct` avoids that.

### Failure propagation: sensor stale -> curve error -> onSensorFailure

**Decision**: a curve fails when its sensor is stale (no successful reading within `sensorStaleTimeout`, or none at
all), when the sensor or a sub-curve doesn't exist, or when the evaluation panics. The error wraps
`sensors.ErrSensorStale` where that applies. A `function` curve fails if **any** of its sub-curves failed in this
cycle, for every function type, including `minimum`. On error, the curve keeps its last successful value, but stores
the error. The fan controller doesn't use the value then. It applies the fan's `onSensorFailure` action (`max` by
default) and tries again on the next cycle.

**Why**: before #551 the behaviour depended on the sensor and curve type, and none of it was fail-safe: linear and
staircase curves used the last average forever, a file sensor error counted as 0 °C, a PID curve stopped its
controller. Failing the whole function curve is deliberate: a `maximum` over two sensors must not silently ignore the
one that can't be read (comment in `FunctionSpeedCurve.evaluate`).

**Who benefits**: safety. A broken or vanished sensor never makes a fan slow down. Users who prefer the BIOS to take
over can set `onSensorFailure: restore`.

**Alternatives considered**: ignoring failed sub-curves in function curves (rejected, see above); keeping the last
value (the old behaviour, not fail-safe).

**Trade-offs**:

- One flaky sensor in a function curve drives every fan that uses that function curve to `maxPwm`.
- Keeping the last value on error means `CurrentValue()` reports a stale value without saying so. The REST API shows
  the `error` field next to `value`. The Prometheus collector reads `CurrentResult()`: it exports
  `fan2go_curve_error` (1 while the last evaluation failed) and omits `fan2go_curve_value` then, so a dashboard
  doesn't show the old value while the fans run at max.
- There is no hysteresis for a sensor that flaps between stale and fresh.

### Panics are contained per curve

**Decision**: `CurveEvaluator.evaluate` recovers a panic in one curve, logs it with a stack trace and stores it as
that curve's error (`resultStore.storeResult`). The other curves are still evaluated. If the evaluator itself panics
outside a curve, `startCurveEvaluator` (`internal/daemon.go`) restarts it after one second.

**Why**: the fan controllers only update their fans on evaluator cycles. Without the restart, a dead evaluator would
freeze all fans at their last speed. With it, the fans pause for about one second. With the panic stored as a curve error, the affected fans go to their `onSensorFailure` action.

**Who benefits**: safety, and people running fan2go headless, where nobody notices a crashed goroutine.

### Each curve type keeps its own state; reload starts over

**Decision**: curve state (`Value`, `LastTemp`, the PID loop) lives in the curve object, guarded by the curve's own
mutex. A config reload (`SIGHUP` or a change to the config file) stops the evaluator after the fan controllers,
builds new sensors, curves and a new evaluator (`applyConfiguration`), and starts them. The new config is read and
validated first, while the old workers keep running; an invalid config changes nothing. A valid one stops all old
workers, and the fan controllers hand their fans back like on exit. If creating the new objects fails, fan2go
restarts the old sensors, curves, evaluator and fan controllers. The curve state is kept then, but the fans are
handed back and taken over again like on a restart. See [daemon.md](daemon.md#config-reload).

**Why**: per-curve locks let the REST API and Prometheus read concurrently without blocking each other. Building
everything new on reload is simple and avoids mixing old and new configuration.

**Trade-offs**: after a successful reload, every curve starts from scratch: a PID curve loses its integral (first
step is P only), a staircase curve forgets its hysteresis state, and curves report `ErrCurveNotEvaluated` until the
first cycle (the controllers wait for it). This also happens for curves whose config didn't change.

### PID curve restarts after a stale period

**Decision**: if the time between the reading of the last step and the new reading is longer than
`sensorStaleTimeout`, the PID curve calls `PidLoop.Reset()` before stepping.

**Why**: otherwise the whole outage would be applied as one large `dt`, and the I term would jump (#551).

**Trade-offs**: after the reset the loop starts over: the first step is P only, and the I term builds up again from
zero. A curve that held a steady speed through its I term runs lower (or higher) until the integral has built up
again, which takes many steps with a small `i`. During the outage the fan ran its `onSensorFailure` action (`maxPwm`
by default), so the fan comes from there. The `pid` control algorithm has no such reset yet, so after an outage its
first cycle can jump as well (see [control-algorithms.md](control-algorithms.md), "Known limitations").

### Percent values for steps

**Decision**: step values accept a plain number (`0..255`) or a percent value. `0%` is `0` (off), `1%` is `1`
(minimum speed, mapped to `minPwm`), `100%` is `255` (#390).

**Why**: `0..255` was confusing for users who think in percent (#387). Keeping `1%` equal to `1` keeps the meaning
"lowest running speed" of value `1` in the PWM mapping.

**Trade-offs**: percent values are converted when the config is loaded. The REST API shows the converted `0..255`
values, not the original strings.

### Staircase as a separate type with step-down hysteresis

**Decision**: the staircase curve holds a fixed speed per temperature range and only steps down after the
temperature has fallen `hysteresis.down` degrees below the current step (#486).

**Why**: a fan that turns on at one temperature and off at another, without ramping up and down on small changes
(#444). A linear curve with smoothing still follows every change.

**Who benefits**: users who care about noise more than about the exact temperature.

**Trade-offs**: no hysteresis for stepping up. The hysteresis compares against the temperature of the current step,
not against the temperature at which the step was entered. The measured temperature is truncated to whole degrees,
so the curve steps down after a drop of slightly less than `down` (up to one degree less).

## Configuration

Curve definitions (under `curves:`):

| Key                              | Default | Notes                                                                  |
|----------------------------------|---------|------------------------------------------------------------------------|
| `id`                             | -       | Required, unique.                                                      |
| `linear.sensor`                  | -       | Required, must reference a sensor.                                     |
| `linear.min`, `linear.max`       | `0`     | Degrees, integers. Used only without `steps`, then `min < max`.        |
| `linear.steps`                   | -       | Map degrees -> `0..255` or `0%..100%`.                                 |
| `staircase.sensor`               | -       | Required.                                                              |
| `staircase.steps`                | -       | Required, same format as `linear.steps`.                               |
| `staircase.hysteresis.down`      | `0`     | Degrees.                                                               |
| `pid.sensor`                     | -       | Required.                                                              |
| `pid.setPoint`                   | `0`     | Degrees, must be finite.                                               |
| `pid.p`, `pid.i`, `pid.d`        | `0`     | Not all three may be `0`. Negative gains cool; all positive: warning.  |
| `function.type`                  | -       | `minimum`, `maximum`, `average`, `delta`, `sum`, `difference`.         |
| `function.curves`                | -       | At least 2 curve ids, no self-reference, no cycles.                    |

Exactly one of `linear`, `staircase`, `pid`, `function` must be set per curve. An unused curve logs a warning.

Related global and fan options:

| Key                               | Default                                     | Effect on curves                             |
|-----------------------------------|---------------------------------------------|----------------------------------------------|
| `fanController.adjustmentTickRate`| `200ms`                                     | Rate of the curve evaluator cycle.           |
| `tempSensorPollingRate`           | `200ms`                                     | How often new readings arrive.               |
| `tempRollingWindowSize`           | `10`                                        | Smoothing of the average (linear, staircase).|
| `sensorStaleTimeout`              | `max(10s, 10 x tempSensorPollingRate)`      | When a curve fails because of a stale sensor.|
| fan `onSensorFailure`             | `max`                                       | What a fan does while its curve fails.       |
| fan `useUnscaledCurveValues`      | `false`                                     | Use curve values as raw PWM values.          |

## Known limitations and open questions

- Positive PID gains are accepted with a warning (heating use cases). With them, the fan speeds up when the
  temperature is below `setPoint`.
- The staircase hysteresis truncates the measured temperature to whole degrees, so the curve steps down after a drop
  of up to one degree less than `down`. Example: step 50, `down: 5`, it steps down at 45.9 °C.
- `fan2go curve list` prints nothing for `staircase` curves, and no graph for `min`/`max` linear curves.
- One tick rate for all curves and fans. A per-fan or event-driven pipeline was left out of #553.
- A reload resets all curve state, even for unchanged curves.
- A flapping sensor (stale, fresh, stale) notifies on every flap and, with `onSensorFailure: restore`, switches the
  control mode every time. No hysteresis exists for this yet.
- Open question: should a `function` curve of type `maximum` keep working with the remaining sub-curves, or fall
  back to `255` for the failed one, instead of failing as a whole? The current choice favours safety.
