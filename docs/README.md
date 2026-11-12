# fan2go design documentation

These documents describe how fan2go's core systems work and why they work that way: the decisions behind them,
the alternatives that were rejected, who benefits, and the known limitations. They are written for contributors
and for advanced users who want to understand fan2go's behaviour. For installation and configuration, see the
[main README](../README.md).

The documents describe the current state of the `master` branch. When you change one of these systems, update the
matching document in the same pull request.

## Data flow

```
sensors ──> sensor monitors ──> curve evaluator ──> fan controllers ──> fans
            (poll, smooth,      (every curve once   (control algorithm,
             detect stale)       per cycle)          pwmMap, safety checks)
```

## Documents

| Document | Covers |
|---|---|
| [Daemon](daemon.md) | Startup, reload, shutdown and signals, failure policy, concurrency, time handling, config validation, statistics, REST API, notifications, build variants, versioning |
| [Sensors](sensors.md) | Sensor types, how hwmon/NVIDIA/disk sensors are found, polling, moving averages, stale detection, `cmd` sensors |
| [Curves](curves.md) | Curve types (`linear`, `staircase`, `pid`, `function`), value range, the central curve evaluator, failure propagation |
| [Control algorithms](control-algorithms.md) | `direct` and `pid`, their defaults and tick-rate dependence, seeding at the curve value, recovery after a curve failure or a gap |
| [Fan controller](fan-controller.md) | The per-fan runtime loop, PWM limits and `pwmMap`, control modes, restore on exit, sensor and write failures, `neverStop` stall detection |
| [Fan analysis](fan-analysis.md) | The initialization sequence, `pwmMap` detection, the PWM to RPM curve, persistence, `fan2go fan init` |
