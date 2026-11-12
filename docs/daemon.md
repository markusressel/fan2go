# The fan2go daemon: lifecycle and cross-cutting design

This document describes how the fan2go daemon starts, runs, reloads its configuration and shuts down, and the design
decisions that apply to all subsystems: failure handling, concurrency, time, config validation, the Prometheus
exporter, the REST API, notifications, build variants and versioning. The daemon is the default command of the CLI
(`fan2go` without a subcommand, `cmd/root.go`). Its lifecycle lives in `internal/daemon.go`, object creation in
`internal/factory.go`, the sensor monitor in `internal/monitor.go` and the webservers in `internal/webservers.go`.

Details of the individual subsystems are in their own documents:

- [Sensors](sensors.md): sensor backends, moving averages, staleness.
- [Curves](curves.md): curve types and the central curve evaluator.
- [Control algorithms](control-algorithms.md): `pid` and `direct`.
- [Fan controller](fan-controller.md): the per-fan control loop, sensor failure handling, restore on exit.
- [Fan analysis](fan-analysis.md): fan initialization, `pwmMap`/`setPwmToGetPwmMap` detection, RPM curve
  measurement, persistence and `fan2go fan init`.

## How it works

### Startup sequence

1. **Read the config file** (`configuration.DetectAndReadConfigFile`). The path comes from `-c`, otherwise viper
   searches `fan2go.yaml` in `.`, `$HOME` and `/etc/fan2go/`. A missing file or a YAML syntax error exits with code 78.
2. **Load and validate** (`configuration.LoadConfig`, `configuration.ValidateConfig`). Errors are logged once, sent as
   a desktop notification and make the process exit with code 78 (`loggedError` in `cmd/root.go`, see "Exit codes").
3. **Check the process owner** (`checkProcessOwner`). Running as non-root only logs a hint.
4. **Create the objects** (`InitializeObjects`):
   - `hwmon.GetChips()` scans the hwmon chips once.
   - Sensors: hwmon sensors are resolved against the scanned chips. A sensor that can't be resolved or created is
     skipped with a warning and a notification. Every created sensor gets a first reading to seed its moving average;
     if that fails, the sensor stays stale until the sensor monitor reads it.
   - Curves: an error here aborts the startup with exit code 78, like curve evaluator errors in step 5.
   - Fans: like sensors, a fan that can't be resolved or created is skipped with a warning and a notification.
   - NVIDIA devices are resolved the same way, through NVML (only in builds with NVML support).
5. **Create the fan controllers and the curve evaluator** (`initializeFanControllers`). The evaluator gets all curves.
   Each fan gets a `controller.DefaultFanController` that subscribes to the evaluator's cycles. The fan controllers
   are added to the registry (for the REST API), and only then the Prometheus collectors of all new objects replace
   the previous ones (`statistics.ReplaceAll`). A curve evaluator error (e.g. a curve dependency cycle) exits with
   code 78, a collector registration error with code 1.
6. **Subscribe to the signals** (`subscribeSignals`), before any fan controller starts. From here on, `SIGINT`/`SIGTERM`
   never kill the process with their default action.
7. **Start the actors** (`RunDaemon`, an `oklog/run` group): the orchestrator, the signal handler, the config file
   watcher and, if enabled, the profiling webserver.
8. If no fan is left after step 4, the orchestrator returns an error and fan2go exits with code 1 ("no valid fan
   configurations, exiting"). This is not a config error: the fans may have been skipped because their hardware isn't
   available yet, so systemd restarts fan2go.

Between steps 5 and 6, `forgetOriginalStatesOfUnconfiguredFans` deletes the saved original states (see
[fan-controller.md](fan-controller.md)) of fans whose id is not in the config anymore. Fans that are configured but
couldn't be created (e.g. their hwmon chip wasn't found) keep theirs. Such an entry means that an earlier run didn't
hand the fan back completely (it was killed, or its restore failed), so each one is logged once as a warning, with the
saved control mode and PWM, so the user can check that fan. This runs only at startup: on reload, the stopped
controllers delete the entries of their fans when they hand them back.

Each fan controller first creates the database directory (`Persistence.Init`). Then it waits before it touches its
fan: `startupSensorWarmup` (2 s) plus one `tempSensorPollingRate`, so the sensor monitors have fresh readings. It then
runs the fan initialization if needed, loads the saved curve and PWM mappings (which can still run the 0..255
`setPwmToGetPwmMap` sweep if that map is missing, see [fan-analysis.md](fan-analysis.md)), takes manual control, and
waits `startupUpdateDelay` (1 s) before its first speed update. A database read error stops that fan's controller,
not the daemon. Both delays are fields of the controller with fixed defaults; they are injectable for tests
(`NewFanController`), not configurable by users.

### Goroutines at runtime

```
RunDaemon (run.Group)
├── orchestrator ── per config generation ("cycle"):
│     ├── sensor monitor (1 per sensor)      reads the sensor every tempSensorPollingRate
│     ├── curve evaluator (1)                evaluates all curves every fanController.adjustmentTickRate,
│     │                                      then notifies the subscribed fan controllers
│     ├── fan controller (1 per fan)
│     │     ├── RPM monitor                  every rpmPollingRate (fans with an RPM sensor)
│     │     └── update loop                  one update per evaluator cycle, the only writer to the fan
│     └── REST API / statistics servers     if enabled
├── signal handler                           SIGINT/SIGTERM: shut down, SIGHUP: reload
├── config file watcher                      content change: reload (watches the config directory)
└── profiling webserver                      if enabled (pprof)
```

Data flows one way: sensor monitors write readings and moving averages into the sensors, the evaluator reads them and
stores one result per curve, and each fan controller reads its curve's result after every cycle. The API and the
statistics read the same stored values. Shared objects live in the `registry.Registry` (concurrent maps of fans,
sensors, curves and, for the REST API, the fan controllers).

### Config reload

`SIGHUP` or a change of the config file content triggers a reload (`runOrchestrator`, `waitForReload`):

1. While the current cycle keeps running, `loadNewConfiguration` re-reads, parses and validates the file. If that
   fails, the error is logged and notified ("Reload failed: ... Keeping current configuration."), and nothing else
   happens: no fan is touched. viper replaces its in-memory config only after a successful parse, and
   `configuration.CurrentConfig` is not changed.
2. If a fan controller is analyzing its fan (`FanController.IsInitializing`: the fan initialization or the
   `setPwmToGetPwmMap` sweep runs because there is no usable saved data), the reload is deferred ("Config reload
   deferred until the analysis of fan(s) ... has finished.") and checked again every second. Another reload request
   meanwhile reads the file again: a valid file replaces the deferred config, an invalid one drops it. A shutdown is
   never deferred, and wins over a reload request that arrives at the same time. A shutdown while step 4 runs
   doesn't start the new cycle.
3. The current cycle is cancelled. The orchestrator waits until all fan controllers, sensor monitors and webservers
   have stopped, then stops the curve evaluator. Every fan controller restores its fan on the way out (`onExit`).
4. `applyConfiguration` makes the validated config the current one and creates new objects from scratch (hwmon
   scan, registry, curves, fans, controllers, evaluator, collectors).
5. On success, the new cycle starts. A warning is logged for each key that only applies on startup (`dbPath`,
   `profiling.*`), if this reload changed it and it differs from the value fan2go was started with. If the objects
   can't be created, the error is logged and notified, the old config stays current, and the previous objects are
   started again. Each restarted fan controller resets its runtime state first (`resetRuntimeState`), so it
   controls the fan like a new one. It keeps the fan state from before fan2go took over the fan, whether the fan
   still has to be handed back, what it knows about the saved original state in the database (see
   [fan-controller.md](fan-controller.md)), its cumulative statistics counters and its log-once flags. If its last
   restore was incomplete, that counts as a write: `Run` then restores the fan also on early errors.

Fan controllers don't survive a reload. Every fan is handed back to its pre-fan2go state and taken over again by a
new controller after the startup delays.

The config file watcher (`runFileWatcher`) watches the directory of the config file, not the file itself, so it also
sees atomic saves (a temporary file renamed over the config, as vim, most GUI editors, `sed -i` and Ansible do).
Events of other files in the directory are ignored. If the config file is a symlink, the directory of its target is
watched too (re-resolved after each change), and any change in the directory of the symlink is considered. After the
last change, the watcher waits 500 ms (`configWatchDebounce`), then hashes the file and requests a reload only if
the content changed. An inotify queue overflow also makes it check the file. If the watcher can't be set up (e.g. the
inotify limits are reached), stops, or the config directory itself is removed or renamed (inotify drops the watch
then), it warns and notifies once ("Automatic config reload is disabled"), and the daemon keeps running; `SIGHUP`
still reloads. The watcher is not set up again until fan2go restarts.

### Shutdown

`SIGINT` or `SIGTERM` makes the signal handler return, which cancels the shared context. Every fan controller stops
its update loop and RPM monitor and restores its fan (`restoreControlModeOnce`, see
[fan-controller.md](fan-controller.md)). The orchestrator waits for all of them, and the profiling webserver shuts
down. `RunDaemon` returns, `main` shuts NVML down (`nvidia_base.CleanupAtExit`) and exits with code 0.

## Design decisions

### Fail early on config errors, but skip missing hardware

**Decision:** The config is checked in two stages. `ValidateConfig` checks everything that can be checked without
hardware: references between sensors, curves and fans, curve dependency cycles, value ranges, durations, the file
permissions of the config if it contains `cmd` fans or sensors, and the `nvidia.device` regex. Any error stops the
daemon before it touches a fan, with exit code 78. Hardware lookups happen afterwards; a fan or sensor whose hardware
can't be found is skipped with a warning and a desktop notification, and the rest keeps running. The same validation
is available as `fan2go config validate`.

**Why:** An invalid value used to crash the daemon after it had taken manual control (a `time.NewTicker` panic on a
zero polling rate, NaN moving averages); #540 closed those gaps. Exiting with code 0 on config errors hid the failure
from systemd; #550 changed that to code 1, and a dedicated code (78) then stopped the systemd restart loop (see "Exit
codes"). Missing hardware is different: a single wrong `platform` or a removed fan
made the whole daemon refuse to start, leaving every fan uncontrolled (#70, #415).

**Who benefits:** Users editing their config get the error at load time, from the CLI or in `systemctl status`, not
after the fans spun up. Users with changing hardware (removed fan, unplugged disk) keep control of the remaining fans.

**Alternatives considered:** Ignoring invalid values with defaults was not done: a wrong default for a fan control
setting is a safety problem. Failing on missing hardware was the old behaviour and was rejected (#70).

**Trade-offs:** A skipped sensor is not retried. A curve that uses it can't be evaluated, so its fans apply their
`onSensorFailure` action. A device that appears late during boot (#510) stays skipped until the next reload or restart.
Unknown config keys only produce a warning (`WarnUnknownKeys`), so typos in optional keys load silently with defaults
apart from that warning.

### Exit codes: 78 for config errors, returned errors instead of `os.Exit`

**Decision:** fan2go exits with one of three codes, defined in `cmd/exitcodes.go`:

| Code | Meaning |
|---|---|
| 0 | Stopped normally (`SIGINT`/`SIGTERM`), or the command succeeded |
| 1 | Any other error: no fan left after skipping missing hardware, the statistics collectors can't be registered, a failing file watcher, an invalid command line, errors of other commands |
| 78 | Config error (`EX_CONFIG` from `sysexits.h`): the config file can't be found, read, decoded or validated, or the daemon can't create its curves or the curve evaluator from it |

Errors caused by the config are marked as `configuration.ConfigError` (`InitConfig`, `ReadInConfig`, `LoadConfig`,
`ValidateConfig`, and the curve and curve evaluator creation in `RunDaemon`; sensors and fans that can't be created
are skipped there). An error registering the Prometheus collectors (`statistics.ReplaceAll` in
`initializeFanControllers`) is not caused by the config and exits with 1. The other commands create single objects for display (e.g.
`fan2go fan curve`); an error there exits with 1. Every command returns its errors to cobra (`RunE`); `cmd.Execute` prints
errors that weren't logged yet and maps them to the exit code (`exitCode`); `main` shuts NVML down and calls `os.Exit`.
No other code calls `os.Exit` or a panicking log function. The codes are the same for all commands, e.g.
`fan2go config validate` exits with 78 for an invalid config.

**Why:** Object creation errors went through pterm's `Fatal` printer, which panics: exit code 2 with a Go stack trace
for a config mistake. `os.Exit` in `RunDaemon` skipped the NVML shutdown deferred in `main`. With one code for all
errors, systemd restarted fan2go every 10 s forever on an invalid config, with a desktop notification each time; a
dedicated code lets the unit stop that (`RestartPreventExitStatus=78`).

**Who benefits:** Users running fan2go as a service: an invalid config shows up once as a failed unit instead of a
notification every 10 s. Scripts and packagers can tell config errors from other failures.

**Alternatives considered:** A start limit (`StartLimitBurst`) would also stop the loop on config errors, but also
after real crashes, and only after several notifications. Code 78 for "no fan found" was rejected: the hardware may
appear later (#510), so a restart can help.

**Trade-offs:** After fixing the config, the user has to restart the unit; it isn't retried and the file watcher isn't
running. Exit code 1 for config errors (#550) was never released, so 78 is the first change since 0.16.0 (exit code 0
for config errors of the daemon, 1 for the other commands).

### The actor model: one orchestrator, independent workers

**Decision:** Each subsystem runs in its own goroutine and is stopped through a `context.Context`. The top level is an
`oklog/run` group: when one actor returns, all are interrupted. The orchestrator owns one "cycle" per config
generation and stops it as a whole. The curve evaluator has its own context, which is cancelled only after the fan
controllers have stopped.

**Why:** Reload and shutdown need a single, ordered stop path. Fan controllers are driven by evaluator cycles; stopping
the evaluator last means controllers never wait on a stopped evaluator while they shut down (they don't need it for the
restore either). The structure came with the SIGHUP reload (#497) and the file watcher (#498).

**Who benefits:** Maintainers: every worker has the same lifecycle (start with ctx, stop on cancel, wait on a
`WaitGroup`). Users: a reload and a shutdown restore the fans in the same way.

**Trade-offs:** Any top-level actor that returns stops the whole daemon. Optional actors must therefore not return on
their own errors: the file watcher warns and then waits for the shutdown instead.

### Reload rebuilds everything

**Decision:** A reload reads, parses and validates the new config while the old objects keep running. A config that
fails this keeps the old objects running untouched. A valid config stops all fan controllers (which restore their
fans), then every object is created again. If the objects can't be created, the old objects are restarted with a
reset runtime state, and `configuration.CurrentConfig` keeps the old value.

**Why:** Rebuilding avoids reconciling old and new state (fan IDs, curve graphs, PID state, collectors). An earlier
version kept the controllers and swapped their curves (#497); the code on master rebuilds them. Validation on reload
was added in #540: before, an invalid config was applied on SIGHUP and could crash the daemon. Validating before
the old cycle stops means that a typo in a saved config doesn't hand every fan back to the BIOS for a few seconds.
The objects are still created after the old cycle stopped: object creation swaps the global
`configuration.CurrentConfig`, which the running workers read without a lock.

**Who benefits:** Users who tune curves or PID values (#424) and see the effect without `systemctl restart`.

**Trade-offs:** During a reload each fan is briefly back in its original mode (usually automatic) for the startup
delays, about 3 s plus `tempSensorPollingRate`, and longer if a new fan needs its initialization. A reload doesn't
cancel a running fan initialization (it would start over, since the RPM curve is saved only after a complete run):
it is deferred until no fan is analyzed anymore, which can take a few minutes on the first start. Every reload resets
settings that the controller learned at runtime (e.g. the minPwm offset of a stalling fan), also when the old
controllers are restarted after a failed object creation. The profiling webserver and the persistence are created
once in `RunDaemon`, so `profiling` and `dbPath` changes need a restart; a reload logs a warning for them.

**Alternatives considered:** Keeping the controllers of unchanged fans running (a diff-based reload) would avoid the
handback and the deferral, but needs per-object reconciliation. Letting a second `SIGHUP` force a deferred reload was
not done: the analysis ends on its own, and the extra path adds little.

### Signals: restore before exit, ignore repeated signals

**Decision:** `RunDaemon` subscribes to `SIGINT`, `SIGTERM` and `SIGHUP` (`subscribeSignals`) before the actors start,
so a signal that arrives while the fan controllers start is queued for the signal handler. The subscription is never
stopped and the channel never closed. After the first `SIGINT`/`SIGTERM`, further signals are logged ("waiting for the fan settings to be restored
(send SIGKILL to force exit)") and ignored until the process exits. A `SIGHUP` while a reload is pending is dropped
(the reload channel has a buffer of one).

**Why:** Closing the channel made a second signal panic with `send on closed channel`, and `signal.Stop` would let the
default action of a second `SIGTERM` kill the process. Both skipped the restore and left fans in manual mode at the
last PWM value (#548).

**Who benefits:** Users who press Ctrl-C twice, and systemd setups where a stop and a reload overlap.

**Trade-offs:** Only `SIGKILL` forces an immediate exit. A shutdown that hangs (e.g. a stuck `cmd` fan script) waits
for the command timeouts; with systemd, `TimeoutStopSec` ends it eventually. `SIGKILL` and OOM kills are not handled
at all.

### Panic recovery: a failing part must not take down the other fans

**Decision:** Panics are recovered where they can happen during normal operation:

- Fan controller (`Run`, RPM monitor, update loop): the panic is logged with a stack trace and returned as an error.
  The fan is restored if fan2go may have written to it. The other fans keep running.
- Sensor monitor: a panic while reading a sensor counts as a failed reading; the monitor keeps running.
- Curve evaluator: a panic in one curve is stored as that curve's error, so its fans apply `onSensorFailure`. A panic
  of the evaluator itself restarts it after 1 s (`startCurveEvaluator`).
- REST API: echo's `Recover` middleware. Statistics: `net/http` recovers handler panics.

**Why:** A panic in any goroutine used to end the process without restoring the fans (#548).

**Who benefits:** Everyone with more than one fan, and users of `cmd` fans and sensors, whose scripts are outside
fan2go's control.

**Trade-offs:** A fan whose controller stopped after a panic or error stays restored (uncontrolled by fan2go) until
the next reload or restart. The user gets a "Fan Controller: <id>" notification. Panics outside these goroutines
(e.g. in the orchestrator itself) still end the process.

### systemd: hardened unit, restart on any exit

**Decision:** `fan2go.service` runs as root with `Restart=always`, `RestartSec=10` and `RestartPreventExitStatus=78`
(no restart after a config error, see "Exit codes"), passes the config with `-c`
and `--no-style`, and restricts the process: `ProtectSystem=strict` with write access to `/sys/class/hwmon`,
`/sys/class/thermal`, `/sys/devices` and the state directory `/var/lib/fan2go`, device access for hwmon, i2c and
NVIDIA devices, network limited to localhost, a system call filter, `MemoryMax=256M`, `TasksMax=50`.

**Why:** fan2go needs root for sysfs writes, but nothing else of root's power. `Restart=always` brings fan control
back after a crash. A config error can't be fixed by a restart, so it ends the restarts; `systemctl status` shows the
failed unit and the error.

**Who benefits:** Packagers and users running fan2go headless.

**Trade-offs:** After a config error, the user has to restart the unit once the config is fixed. Other errors (e.g. no
fan found) are still retried every 10 s forever. The unit's `IPAddressAllow=localhost` blocks remote Prometheus
scrapes and API clients even when `statistics.host` or `api.host` is `0.0.0.0`; users need a drop-in override with
`IPAddressAllow=`. A `dbPath` outside `/var/lib/fan2go` is read-only under `ProtectSystem=strict` and needs
`ReadWritePaths=`. Both are documented in the README ("As a Service"). NVIDIA access needed manual overrides in the past
(#472, #510).

### Concurrency: leaf locks, one writer per fan, `-race` in CI

**Decision:** Shared state is protected at the object that owns it:

- Each fan has a `sync.RWMutex` around its mutable fields. It is a leaf lock: no I/O and no other fan method runs
  under it (#544).
- Each curve stores its result under its own lock; only the curve evaluator evaluates curves (#553).
- The fan controller's statistics are guarded by a mutex; `GetStatistics` returns a copy.
- The REST API serializes fans, sensors and curves through `MarshalJSON` methods that take a snapshot under the lock.
- Per fan, the update loop is the only goroutine that writes to the fan.
- `configuration.CurrentConfig` is a plain global. Workers read it without a lock. It is only replaced during a reload,
  while all workers of the cycle are stopped.

`make test` runs `go test -race -tags disable_nvml ./...`, and CI runs `make test`, so every pull request is checked
for data races.

**Why:** The RPM monitor, the update loop, the statistics and the API used to access the same fans without
synchronization (#544). Shared curves were evaluated by every fan that used them, concurrently (#553).

**Who benefits:** Maintainers: the race detector catches regressions. Users: no torn values in the API or metrics.

**Trade-offs:** The `CurrentConfig` invariant ("only written while everything is stopped") is a convention, not
enforced by the type system. NVML code is excluded from the race-checked test run (`disable_nvml`).

### Time is injected

**Decision:** Code that waits or measures time uses `util.Clock` (`Now`, `After`, `NewTicker`). Production uses
`util.RealClock`; tests use `util.FakeClock`, which only moves on `Advance`. The fan controller, the sensor monitor,
the curve evaluator and the fan analysis take a clock; the control loops get the controller's time (`now`) on every
`Cycle`. All waits in the controller are context-aware (`sleepWithContext`), so a shutdown during a startup delay
or a PWM sweep returns right away (#542).
User-facing delays are config keys: `fanResponseDelay` (number of seconds or a duration), `fanController.pwmSetDelay`
(global and per fan), the `analysis.*` delays and the `timeout` of `cmd` fans and sensors.

**Why:** Time-based behaviour (warm-up, stall detection, staleness, PWM write failure timeouts) was tested with real
sleeps or not at all. Hard-coded timeouts didn't fit slow `cmd` scripts.

**Who benefits:** Maintainers: deterministic, fast tests. Users with slow fans or scripts: tunable delays.

**Trade-offs:** The startup delays (2 s warm-up, 1 s update delay) and the PWM write failure timeout (10 s) are
constants. Not every component takes a clock yet (e.g. the evaluator restart delay). The notification worker can't
use `util.Clock` (`util` imports `ui`); its wait is a replaceable function (`workerWait`) instead.

### Statistics: local only, no hardware reads on scrape

**Decision:** The Prometheus exporter (`statistics.enabled`) listens on `127.0.0.1:9000` by default and serves
`/metrics`. A scrape only reads values fan2go maintains anyway:

| Metric | Meaning |
|---|---|
| `fan2go_sensor_value{id}` | Moving average of the sensor, as used by the curves; missing until the first successful reading |
| `fan2go_sensor_stale{id}` | 1 while the curves reject the sensor value (no reading yet, or older than `sensorStaleTimeout`) |
| `fan2go_sensor_last_reading_timestamp_seconds{id}` | Unix time of the last successful reading; missing until there is one |
| `fan2go_sensor_read_failures_total{id}` | Failed readings since the sensor was created (start or reload) |
| `fan2go_curve_value{id}` | Value of the curve's last evaluation; missing while it failed and before the first one |
| `fan2go_curve_error{id}` | 1 if the curve's last evaluation failed, 0 if it succeeded; missing before the first one |
| `fan2go_fan_pwm{id}` | Raw PWM value (after the `pwmMap`) the controller last set; missing until it set one |
| `fan2go_fan_rpm{id}` | RPM moving average; missing until the RPM monitor seeded it |
| `fan2go_controller_unexpected_pwm_value_count{id}` | Times the fan's PWM didn't match what fan2go set |
| `fan2go_controller_increased_minPwm_count{id}` | Automatic minPwm increases because the fan stalled |
| `fan2go_controller_minPwm_offset{id}` | Current offset added to the fan's minPwm because of stalls |
| `fan2go_controller_state{id,state}` | 1 for the controller's current state, 0 for the others (see [fan-controller.md](fan-controller.md#controller-state)) |

The default Go and process collectors of `client_golang` are exported as well. The fan2go collectors are created
for the objects of a config after all of them exist, and then replace the previous collectors in one step
(`statistics.ReplaceAll`). A reload that fails before that keeps the collectors of the old objects, which are
restarted.

A sensor keeps exporting its last value while it is stale: it is a real past measurement, and `fan2go_sensor_stale`
says it is old. A failed curve exports no value: its last good value no longer controls the fans (they are at max
or handed back), so a dashboard would look normal while it isn't. The controller state is one enum-style metric
(the usual Prometheus pattern for a state set), so `fan2go_controller_state{state=~"stopped_.+"} == 1` alerts on
every controller that stopped because of an error.

**Why:** The exporter used to listen on all interfaces and read every sensor and fan on every scrape: anyone who
could reach the port could run `cmd` scripts, SMART ioctls and NVML calls (#550).

**Who benefits:** Everyone with the exporter enabled: scrapes have no side effects. Users on shared networks.

**Alternatives considered:** Binding to all interfaces by default was rejected for the reason above.

**Trade-offs:** This is a breaking change for remote scrapes: set `statistics.host: 0.0.0.0` (and override the systemd
network restrictions). Values lag slightly behind the hardware. Missing samples (`fan2go_sensor_value` before the
first reading, `fan2go_curve_value` while the curve fails) need `absent()` or the matching status metric in alerts.
There are no metrics for failed fan RPM reads or PWM writes yet.

### REST API: read-only, local, off by default

**Decision:** The REST API (`api.enabled`, default `127.0.0.1:9001`) serves `GET /alive`, `/fan`, `/fan/<id>`,
`/sensor`, `/sensor/<id>`, `/curve`, `/curve/<id>`, `/controller` and `/controller/<fan id>`. `POST /fan` and
`DELETE /fan/<id>` exist but return an error ("not yet supported"). There is no authentication.

Besides their state, sensors show their reading status (`lastReadingTime`, `consecutiveFailures`, `readFailures`,
`stale`), curves the result of their last evaluation (`error`, `evaluatedAt`), and `/controller` the state of each
fan controller (`fanId`, `state`, `since`, `error`, see [fan-controller.md](fan-controller.md#controller-state)).

**Why:** It lets UI clients show the daemon's state. Without authentication, binding to localhost is the safe default.

**Trade-offs:** The JSON shape is the internal structure of fans, sensors and curves, so internal refactorings can
change it; golden-file tests pin the output (#544). The example `fan2go.yaml` and the README show `host: localhost`,
the code default is `127.0.0.1`.

### Notifications: tell the desktop user, never block the control path

**Decision:** Errors that need the user's attention (config errors, skipped fans and sensors, a stopped fan controller,
webservers that can't start, fatal errors) are logged and sent as desktop notifications via `notify-send`
(`internal/ui/notification.go`). fan2go finds graphical sessions with `who` (preferring the `DISPLAY` from its
environment; the unit sets `DISPLAY=:0`). As root, it uses `systemd-run --user --machine=<user>@.host` to reach the
user's session bus. If no session exists yet, notifications are queued (at most 100, the oldest are dropped) and a
background worker retries every second. Every external command has a 5 s timeout. The queue lock is never held while
a command runs, so queueing a notification doesn't wait for the worker.

**Why:** A daemon's log is easy to miss; a failing fan controller is a hardware risk (#79). The timeouts were added
because a hanging D-Bus could block a fan controller that was stopping after an error; the fan is now restored before
the user is notified (#548).

**Who benefits:** Desktop users. Headless systems lose nothing but a few `who` calls.

**Trade-offs:** Notifications depend on `who`, `systemd-run` and `notify-send` being present and permitted by the
unit's sandbox. Queued notifications are kept until a session appears; beyond 100, the oldest are lost (they are in
the log).

### Build variants: `disable_nvml`

**Decision:** NVIDIA support (NVML via cgo) is behind the build tag `disable_nvml`: with the tag, stub files replace
the NVIDIA fans, sensors and the NVML handle. CI builds both variants for every architecture (`make build` and
`make build-no-nvml`, artifacts with the suffix `-no-nvml`). The test suite runs with `disable_nvml`.

**Why:** NVML needs the NVIDIA library at runtime and cgo at build time; users without NVIDIA GPUs and some
architectures don't want that dependency.

**Trade-offs:** NVIDIA code is not covered by `make test`. A config with `nvidia` fans or sensors fails validation in a
`disable_nvml` build ("This version of fan2go was built without NVIDIA (nvml) support").

### Versioning and upgrade notes

**Decision:** fan2go follows semantic versioning, strictly from `1.0.0` on (`RELEASING.md`). Releases up to `0.16.0`
used the pre-1.0 convention. A change is breaking if an existing user may notice it after upgrading without changing
anything: config semantics or defaults, CLI output or exit codes, the REST API or metrics, persisted data, or fan
behaviour. Every PR has a "Breaking changes / upgrade notes" section; breaking PRs get the `breaking-change` label,
which Release Drafter turns into a release notes section and a major version bump. For each release with breaking
changes, an entry is appended to `packaging/aur/fan2go.install`; pacman prints it when a user upgrades across that
version, for both `fan2go` and `fan2go-git`. AUR packages are published manually with `packaging/aur/publish.sh`.

**Why:** fan2go controls hardware; a silently changed default (e.g. `statistics.host`, exit codes, stricter
validation) can break monitoring or stop the daemon after an unattended upgrade.

**Who benefits:** Users of the Arch packages, packagers and anyone reading release notes.

**Trade-offs:** Upgrade notes are a manual step at release time. `fan2go-git` users get the warnings only once the
version is tagged, after they already run the change. The Makefile's `VERSION` default must be bumped by hand.

## Configuration

| Key | Default | Notes |
|---|---|---|
| `dbPath` | `/var/lib/fan2go/fan2go.db` | Persistence of fan analysis results ([fan-analysis.md](fan-analysis.md)), not reloadable |
| `tempSensorPollingRate` | `200ms` | Sensor monitor interval, also part of the startup warm-up |
| `rpmPollingRate` | `1s` | RPM monitor interval |
| `fanController.adjustmentTickRate` | `200ms` | Curve evaluator cycle, i.e. fan update interval |
| `controllerAdjustmentTickRate` | unset | Deprecated alias of the above |
| `fanController.pwmSetDelay` | `5ms` | Wait after a PWM write in the `setPwmToGetPwmMap` sweep and the fan analysis |
| `fanResponseDelay` | `2` (seconds) | Only used by the fan analysis (readback retries, small steps); seconds or a duration |
| `statistics.enabled` / `host` / `port` | `false` / `127.0.0.1` / `9000` | Prometheus exporter |
| `api.enabled` / `host` / `port` | `false` / `127.0.0.1` / `9001` | REST API |
| `profiling.enabled` / `host` / `port` | `false` / `127.0.0.1` / `6060` | pprof, not reloadable |

CLI flags: `-c/--config`, `--no-color`, `--no-style`, `-v/--verbose`. viper's `AutomaticEnv` is enabled, so
environment variables that match a config key override it. Fan, sensor, curve, analysis and failure-handling keys are
described in the subsystem documents.

## Known limitations and open questions

- **The config file watcher doesn't follow every symlink chain.** It watches the directory of the config file and of
  its final target. A change of an intermediate symlink in a third directory is not seen; `SIGHUP` still works. On
  network or FUSE file systems inotify may not report changes at all.
- **A valid reload releases all fans briefly**, even if only an unrelated key changed, and resets runtime-learned
  state. Errors that only object creation detects (not validation) also cause a release and takeover by the old
  controllers.
- **A reload during an analysis waits for it.** Until every running analysis of the current config has finished,
  the old config stays active. Fans that are new in the reloaded config are analyzed after it was applied.
- **`fan2go fan init` next to a running daemon is not prevented.** The initialization lock only works within one
  process, and the database is opened per operation, so nothing stops both from writing to the same fan. Run
  `fan init` with the daemon stopped. A running daemon picks up the new data only after a reload or restart (see
  [fan-analysis.md](fan-analysis.md)).
- **Skipped hardware is never retried** (#510). A device that appears after startup needs a reload or restart.
- **A killed daemon can't hand the fans back.** After `SIGKILL` or an OOM kill, fans stay in manual mode at their
  last PWM until the next run stops; that run restores them to the state saved before the killed run took over (see
  [fan-controller.md](fan-controller.md)). Without a working database, the next run takes the manual state as the
  original one.
- **`CurrentConfig` is global mutable state.** Passing the config into the components is not done yet (out of scope
  of #542).
- **`-c` help text** says the default is `$HOME/.fan2go.yaml`; the actual search is `fan2go.yaml` (or `.yml`, etc.) in
  `.`, `$HOME` and `/etc/fan2go/`.
- **Non-config errors are retried forever** by the systemd unit (every 10 s), e.g. when no configured fan is found.
