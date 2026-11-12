# Sensors and sensor polling

fan2go reads temperatures from sensors (hwmon chips, NVIDIA GPUs, files, external commands and disks). It does not
read them when a curve needs a value. Instead, one sensor monitor per sensor polls the sensor at a fixed rate,
smooths the values and records the last successful reading. Curves only use those recorded values, and a value that
is too old counts as stale.

The code lives in:

- `internal/sensors`: the `Sensor` interface, one type per backend, `ReadingTracker` and `FreshReading`.
- `internal/monitor.go`: the sensor monitor (`sensorMonitor.Run`, `updateSensor`).
- `internal/factory.go`: `initializeSensors` creates the sensors at startup and on reload.
- `internal/hwmon`: discovery of hwmon chips through libsensors and resolution of hwmon sensor configs.
- `internal/nvidia_base`: NVML initialization and device lookup for NVIDIA sensors (and fans).
- `internal/configuration/sensors.go`, `config.go` and `validation.go`: config types, defaults and validation.

What a curve does with a stale sensor and how a fan reacts to it is described in [curves](curves.md) and
[fan controller](fan-controller.md).

## How it works

```
startup / reload (initializeSensors)
  hwmon: resolve platform + index/channel -> sysfs temp input path
  NewSensor(config)                 -> one Sensor object per config entry
  GetValue() once                   -> seeds MovingAvg and the last reading (only on success)

per sensor, one goroutine (sensorMonitor.Run), every tempSensorPollingRate:
  GetValue()
    ok    -> MovingAvg = value (first successful reading)
             or EMA(MovingAvg, value, tempRollingWindowSize)
             RecordReading({value, now})        (resets the failure counter)
    error -> RecordFailure()                    (MovingAvg and last reading unchanged)

curve evaluation (CurveEvaluator, every fanController.adjustmentTickRate):
  FreshReading(sensor, now, sensorStaleTimeout)
    fresh -> linear/staircase use MovingAvg, pid uses the raw last reading
    stale -> error wrapping sensors.ErrSensorStale -> fan applies onSensorFailure
```

Main types:

- `sensors.Sensor`: `GetValue()` reads the hardware. `GetMovingAvg()`/`SetMovingAvg()` hold the smoothed value.
  `RecordReading`, `RecordFailure`, `GetLastReading` and `GetConsecutiveFailures` track the reading state.
- `sensors.ReadingTracker`: embedded in every sensor type. It stores the last successful `Reading`
  (raw value and timestamp) and the number of consecutive failures, guarded by its own mutex.
- `sensors.FreshReading`: returns the last reading if it is not older than the timeout, otherwise an error that
  wraps `sensors.ErrSensorStale`.

All values are in milli-units (millidegrees Celsius for temperatures). Backends that report whole degrees
(NVIDIA, SMART) multiply by 1000.

## Sensor types

Each entry under `sensors:` has an `id` and exactly one of `hwmon`, `nvidia`, `file`, `cmd` or `disk`.
`sensors.NewSensor` picks the type from the sub-config.

| Type | Reads | Value |
|---|---|---|
| `hwmon` | the sysfs `tempN_input` file found through libsensors | integer from the file |
| `nvidia` | NVML `GetTemperature(TEMPERATURE_GPU)` | degrees x 1000 |
| `file` | any file (`~` is expanded to the home of the user running fan2go) | integer from the file |
| `cmd` | stdout of an executable | float, parsed with `strconv.ParseFloat` |
| `disk` | sysfs hwmon of the block device, fallback ATA SMART | millidegrees |

There is also a `VirtualSensor` type in `internal/sensors/virtual.go`. It can't be configured. It was added for
`fan2go curve list`, but the code that uses it is commented out, and the type no longer implements the `Sensor`
interface.

## Design decisions

### Sensors are polled by a monitor, curves never read hardware

**Decision**: Every sensor gets its own monitor goroutine (`startSensorMonitors` in `internal/daemon.go`). The
monitor reads the sensor once right away, then every `tempSensorPollingRate`. Curves, the REST API and the
Prometheus exporter only read the recorded values. They never call `GetValue()`. Together with the seeding read in
`initializeSensors`, each sensor is read twice in quick succession at startup and on reload.

**Why**: Reading a sensor can be slow or block: a cmd sensor may run for up to its timeout, a SMART ioctl talks to
the disk, NVML calls go to the driver. Before #551, the PID curve read its sensor synchronously, and one failed read
stopped the fan controller. Before #550, each Prometheus scrape read every sensor, so anyone who could reach the port
could trigger cmd scripts and SMART ioctls. With a single reader per sensor, the cost of a sensor is fixed by the
polling rate and doesn't grow with the number of curves, fans or scrapes.

**Who benefits**: Users with cmd or disk sensors (no extra script runs or ioctls per curve or scrape). Everyone,
because one slow sensor only delays its own monitor, not the curve evaluation or other sensors.

**Alternatives considered**: Reading sensors on demand in the curve. Rejected in #551: it couples the control
loop to the latency and errors of each backend. An event-driven sensor, curve and fan pipeline is listed as a
possible follow-up in #551, but isn't implemented.

**Trade-offs**: A curve sees a value that is up to one polling period old. One goroutine per sensor. Each sensor
is polled even if no curve uses it (the config validation only warns about unused sensors).

### A read failure is never a value

**Decision**: `GetValue()` returns an error for every failed read. The monitor then calls `RecordFailure()`, and
leaves both the moving average and the last successful reading unchanged.

**Why**: Before #551, a failed file sensor read was reported as 0 °C. The average decayed towards 0, and fans slowed
down or stopped while the real temperature was unknown. A sensor that couldn't be read at startup was seeded with 0
too. Now, `FileSensor.GetValue` returns the error (with the comment "a failed read must never look like a valid
reading"). `initializeSensors` only seeds the average and the last reading if the first read succeeds.

**Who benefits**: Safety. A broken sensor can no longer look like a cold component.

**Trade-offs**: The last good value stays visible as the moving average (e.g. in the API and in
`fan2go_sensor_value`) while the sensor is failing. Consumers must check staleness to know if it is current.

### Staleness: sensorStaleTimeout with a derived default

**Decision**: A sensor value is stale if the last successful reading is older than `sensorStaleTimeout`, or if
there was no successful reading yet (`sensors.FreshReading`). If `sensorStaleTimeout` isn't set, the effective
value is `max(10s, 10 x tempSensorPollingRate)` (`Configuration.GetSensorStaleTimeout`). An explicit value must be
greater than 0. The daemon logs the effective value when it starts the sensor monitors.

**Why**: Before #551, linear and staircase curves kept using the last average forever when a sensor disappeared
(driver unloaded, GPU powered off, disk removed). The only sign was a warning on every poll. A timeout turns "no new
data" into an explicit error that the curves and the fan controller can act on.

The default scales with the polling rate, so a slow polling rate doesn't make sensors stale between two regular
reads. The 10 s floor tolerates about 50 failed polls in a row at the default rate of 200 ms. A cmd sensor that
times out on every run (default timeout 2 s) is stale after about 4-5 runs. The exact factors (10 periods, 10 s) aren't explained in the PR; they look like a conservative
choice for the default polling rate.

**Who benefits**: Safety: fans don't run on a frozen value. Users with flaky or slow sensors can raise the timeout.

**Alternatives considered**: A per-sensor timeout is listed as a follow-up in #551, but isn't implemented.

**Trade-offs**:

- One global timeout for all sensors. A slow cmd sensor and a fast hwmon sensor share it.
- The timestamp of a reading is taken after `GetValue()` returns. For a slow command, the value is older than its
  timestamp.
- No hysteresis: a sensor that alternates between stale and fresh triggers the failure handling each time
  (noted as a follow-up in #551).

### Failures are logged once, not on every poll

**Decision**: `recordSensorFailure` logs only the first failure of a series ("further errors are not logged until
it recovers"). `updateSensor` logs the recovery with the number of failed readings. The sensor monitor sends no
desktop notifications. Notifications come from the fan controller when a curve can't be evaluated, and from
`initializeSensors` when a sensor is skipped at startup.

**Why**: At the default rate of 200 ms, one broken sensor produced 5 warnings per second (#551).

**Who benefits**: People running fan2go headless under systemd: the journal stays readable.

**Trade-offs**: Some backends still log on every failed read themselves. `util.SafeCmdExecution` warns on each
timeout and failed command, and `CmdSensor.GetValue` warns on each unparsable output.

### Panics in a sensor backend are failed readings

**Decision**: `safeUpdateSensor` recovers a panic in `updateSensor`, logs it with a stack trace, and records it as a
failed reading. The monitor keeps running. A panic elsewhere in the monitor stops only that monitor, and `Run`
returns it as an error.

**Why**: The sensor backends include cgo (NVML) and a raw ioctl. Before #548, a panic in a monitor goroutine killed
the whole daemon without restoring the fans.

**Who benefits**: Safety: the other fans stay controlled, and the affected fans go through the normal
sensor failure handling once the value is stale.

### Exponential moving average with a "window size" parameter

**Decision**: After each successful read, the monitor updates the moving average with
`util.UpdateExponentialMovingAvg(old, tempRollingWindowSize, value)`, i.e. `old + (value - old) / n`. If the result is
less than 0.5 away from the new value, the new value is used exactly. The first successful reading replaces the
average instead of being averaged with it.

**Why**: Raw temperature readings jitter, and fans that follow them directly change speed audibly. An EMA needs only
the previous value, not a buffer. The snap to the exact value (#547) lets a constant input reach its value: before,
a temperature that stayed exactly at a staircase step never reached that step. Replacing the average on the first
reading (#551) avoids starting from 0.

**Who benefits**: Users who want quiet, smooth fan changes. Linear and staircase curves use the average.

**Alternatives considered**: A real sliding window over the last n readings. The name `tempRollingWindowSize`
suggests that, but the code uses an EMA. The README explains that older readings still count with decreasing
weight. The reason for an EMA isn't documented; likely simplicity and no buffer per sensor.

**Trade-offs**:

- After a step change, the average covers about 2/3 of the change after n readings, and about 95% after 3n. With
  the defaults (200 ms, n = 10), that is about 2 s and 6 s.
- The PID curve uses the raw last reading, not the average, so it isn't smoothed by this setting (see
  [curves](curves.md)).
- After a recovery, the average continues from the value before the outage. It is not reset like on the first
  reading. With the defaults, it catches up within a few seconds.

### hwmon sensors are resolved by platform regex plus index or channel

**Decision**: An hwmon sensor is configured with a `platform` regex and exactly one of `index` or `channel`
(both >= 1). `hwmon.GetChips` discovers chips and their `tempN` features through libsensors (`gosensors`).
`hwmon.UpdateSensorConfigFromHwMonControllers` then picks the first matching sensor and stores its sysfs input
path in the config. `GetValue()` reads that file directly.

- `channel` is the `N` of `tempN_input`. It comes from the kernel and doesn't change when other inputs appear or
  disappear (#452, issue #286).
- `index` is the 1-based position among the temperature inputs of the chip, in libsensors order. It is the older
  option. The code comment marks it as deprecated in favor of `channel`.
- `platform` is a case-insensitive regex that only has to match part of the name. It is matched against fan2go's
  platform names first (`computeIdentifier`, shown by `fan2go detect`). Only if no sensor matches that way, it is
  matched against the libsensors chip names (`computeLmSensorsName`, as printed by `sensors`) (#550, issue #535).

**Why**: The `hwmonN` directory numbers change between boots, so a fixed sysfs path isn't stable. A platform name
plus a channel identifies the input independently of that numbering. The fan2go names and the libsensors names
differ for PCI and ISA addresses >= 0x1000 (`nvme-pci-05a00` vs. `nvme-pci-5a00`), so a name copied from `sensors`
didn't match before #550. Matching the fan2go names first keeps existing configs resolving to the same sensor.
The libsensors name is formatted in Go, because `gosensors.Chip.String()` could crash on a libsensors error (#550).

**Who benefits**: Users who copy names from `sensors`, users with NVMe drives or other PCI chips at high addresses,
and users whose chips expose several temperature inputs.

**Trade-offs**:

- Resolution happens only at startup and on reload. A chip that appears later (e.g. a module loaded after fan2go
  started) isn't found until the next reload. If the sysfs path disappears, reads fail and the sensor becomes stale.
- If the regex matches several chips, the first chip (in libsensors order) that has the index or channel wins.
  A broad regex like `nvme` can pick a different drive when the chip order changes. Issue #435 shows that chip
  numbering, e.g. `drivetemp-0-*` vs. `drivetemp-13-*`, can change between boots. The `disk` sensor type exists
  for that case.
- A sensor that can't be resolved is skipped with a warning and a "Sensor Skipped" notification. The daemon keeps
  running. Curves that use the sensor then fail with "sensor not found", and their fans apply `onSensorFailure`.
- libsensors is used only for discovery. fan2go reads the raw sysfs value, so `compute` statements from the
  libsensors config don't apply.

### NVIDIA sensors through NVML, optional at build time

**Decision**: `nvidia.device` is a case-insensitive regex matched against identifiers like
`nvidia-10DE2489-0800` (vendor and device ID plus a PCI address computed like libsensors does, see
`nvidia_base.GetDeviceID`). `CreateNvidiaSensor` looks up the device and reads the temperature once. If either
fails, it returns an error and the sensor is skipped. `nvidia.index` is accepted but unused, because NVML exposes
only one GPU temperature (`TEMPERATURE_GPU`). Builds with the `disable_nvml` tag (#396) contain a stub, and config
validation rejects `nvidia` sensors there. Validation also rejects an empty `nvidia.device`, which would match the
first GPU (#550).

**Why**: NVIDIA's proprietary driver doesn't expose GPU temperatures through hwmon. NVML is the official interface.
The build tag lets packagers build fan2go without the NVML dependency.

**Who benefits**: Users with NVIDIA GPUs on the proprietary driver. Packagers and users without NVIDIA hardware.

**Trade-offs**: NVML is initialized once per process (`nvidia_base`). If that fails, e.g. because the driver isn't
loaded yet, it isn't retried on reload. Only a restart finds the GPU then.

### cmd sensors: root-owned executables, timeouts and process groups

**Decision**: A cmd sensor runs `exec` with `args` through `util.SafeCmdExecution` on every poll, and parses
stdout as a float in milli-units. Only newlines are stripped from the output, so trailing spaces or a `\r` make the
read fail. Before each run, the executable must pass
`util.CheckFilePermissionsForExecution`: owned by root, not writable by others, and not writable by its group
unless that group is root. The config file itself must pass the same check if it contains a cmd sensor or fan.
Each run has a timeout (`cmd.timeout`, default 2 s, configurable since #542). The command runs in its own process
group. On timeout, the whole group is killed, and fan2go waits at most 500 ms more for the output (#548).

**Why**: fan2go runs as root, so a command that a regular user can modify would be a privilege escalation. The check was added together with the cmd sensor in #96.
Before #548, a command that started a background process holding its stdout open blocked the call until that
process exited, which also blocked the restore on shutdown. A failed start (e.g. a missing exec bit) panicked.

**Who benefits**: Users who integrate sensors fan2go doesn't support natively (IPMI, network sensors, custom
scripts). Safety: a hanging script can't block fan2go beyond its timeout.

**Trade-offs**:

- Only `exec` is checked. A script passed as an argument to an interpreter (as in the README example with
  `/usr/bin/bash` and a script path) isn't checked.
- At the default polling rate, the command runs 5 times per second. Long-running commands cause skipped polls.
  If a command needs longer than `sensorStaleTimeout` between successful runs, its sensor becomes stale.
  Validation warns if `cmd.timeout` (incl. the default) is not shorter than the effective `sensorStaleTimeout`.
- Side effects of frequent runs (e.g. `nvidia-settings` waking a dGPU) are documented in the README.

### disk sensors: stable device paths, sysfs first, SMART fallback

**Decision**: `disk.device` accepts an absolute path (`/dev/sda`, `/dev/disk/by-id/...`), a name under `/dev`
(`sda`, `disk/by-id/...`) or a bare by-id name (`ata-...`, `nvme-...`). On every read, `DiskSensor.GetValue`
resolves the symlinks to the block device. Then it tries:

1. sysfs hwmon: `/sys/class/block/<dev>/device/hwmon/hwmon*/temp*_input` (SATA with `drivetemp`), and for NVMe also
   `/sys/class/nvme/<controller>/hwmon*/temp*_input`. `temp1_input` is preferred.
2. ATA SMART: an `HDIO_DRIVE_CMD` ioctl with SMART READ DATA. The temperature is the first raw byte of attribute 194
   (temperature) or 190 (airflow temperature), whichever comes first in the attribute table.

**Why**: Issue #435: with an HBA, the `drivetemp` hwmon chips were numbered differently on each boot, so fan2go
failed to start. `/dev/disk/by-id` names are stable, and resolving them on every read follows the device even if
its `sdX` name changes. The SMART fallback (#453) covers SATA disks without `drivetemp`. The SMART buffer layout was
wrong until #550: the sector count was in the wrong byte, so every SMART read failed. The fix follows the kernel and
smartmontools sources. The PR states that it wasn't tested on hardware.

**Who benefits**: Users with many disks, HBAs or hot-swap bays who drive case fans by disk temperature.

**Trade-offs**:

- When sysfs fails, the error of the SMART fallback is returned and the sysfs error is lost.
- The SMART fallback needs read access to the block device (fan2go runs as root). It runs on every poll, i.e. 5
  times per second by default.
- If a drive reports both 190 and 194, the one that comes first in the attribute table wins. On drives that sort
  the table by ID, that is 190.
- It isn't documented whether SMART reads or `drivetemp` reads wake a sleeping disk.

### Sensors are recreated on reload

**Decision**: On a config reload, the daemon first stops the old sensor monitors (`runOrchestratorCycle`), then
`InitializeObjects` creates new sensor objects, resolves hwmon sensors again, and seeds them with one read. Reading
state and averages of the old sensors are dropped.

**Why**: The reload (#498 added reloading on config file changes) rebuilds all objects from the new config instead
of patching them, so changed sensor definitions, polling rates and window sizes take effect without special cases.
The reason isn't documented beyond that; rebuilding is likely the simplest way to keep the objects consistent with
the config.

**Trade-offs**: A sensor whose seeding read fails after a reload has no reading and is stale until the monitor reads
it. The fan controller waits 2 s plus one `tempSensorPollingRate` before it touches a fan (see
[fan controller](fan-controller.md)), so a sensor that the monitor can read within that time doesn't trigger
`onSensorFailure`. If it is still unreadable after that, its fans go through the failure handling.

## Configuration

Global keys:

| Key | Default | Validation | Meaning |
|---|---|---|---|
| `tempSensorPollingRate` | `200ms` | > 0 | Interval of each sensor monitor. |
| `tempRollingWindowSize` | `10` | >= 1 | `n` of the EMA: each reading moves the average by 1/n towards it. |
| `sensorStaleTimeout` | unset: `max(10s, 10 x tempSensorPollingRate)` | > 0 if set | Age after which the last successful reading is stale. |

Plain numbers for `time.Duration` keys are read as milliseconds (`durationMillisecondsHookFunc`). Use units like
`200ms` or `10s`.

Per sensor (`sensors[]`):

| Key | Notes |
|---|---|
| `id` | Unique. Referenced by `linear`, `staircase` and `pid` curves. |
| `hwmon.platform` | Regex, see above. |
| `hwmon.index` / `hwmon.channel` | Exactly one, >= 1. Prefer `channel`. |
| `nvidia.device` | Regex, not empty. Only in builds with NVML. |
| `nvidia.index` | Accepted, unused. |
| `file.path` | Integer in milli-units. `~` is expanded. |
| `cmd.exec`, `cmd.args` | Executable must be root-owned and not writable by others. |
| `cmd.timeout` | Default `2s` (`DefaultCmdTimeout`), `0` means default, negative is rejected. Warning if not shorter than `sensorStaleTimeout`. |
| `disk.device` | Required. Path or by-id name. |

Per fan, `onSensorFailure` (`max` by default, or `restore`) decides what happens to a fan while its curve can't be
evaluated, e.g. because its sensor is stale. It is a fan option, not a sensor option. See
[fan controller](fan-controller.md).

The CLI command `fan2go sensor --id <id>` resolves one sensor like the daemon (#545) and prints a single raw
`GetValue()` result. It doesn't use the monitor, the average or the staleness check.

## Known limitations and open questions

- **Stale state isn't visible from outside.** The REST API serializes sensors without their last reading or
  failure count, and `fan2go_sensor_value` exports the moving average, which is 0 for a sensor that was never read.
  The state exists (`GetLastReading`, `GetConsecutiveFailures`); exposing it is a follow-up from #551.
- **One stale timeout for all sensors**, no hysteresis between stale and fresh.
- **No re-resolution at runtime.** hwmon chips and NVIDIA GPUs that appear after startup need a reload (hwmon) or a
  restart (NVML).
- **cmd sensors:** only the executable is permission-checked, not a script passed as an argument. Timeouts and
  failures are logged on every poll by `SafeCmdExecution`, despite the log-once policy of the monitor.
- **File and cmd parsing differ:** a file sensor needs an integer, a cmd sensor accepts a float.
- **hwmon sensors created by the daemon have no label.** `NewSensor` copies only the index and the input path, so
  the label, channel, min and max of the runtime `HwmonSensor` stay empty or 0 in the API output.
- **Disk sensors** poll SMART at the full polling rate if `drivetemp` isn't loaded. Whether that wakes sleeping disks
  is untested. Attribute 194 isn't preferred over 190: the first of the two in the table wins.
- **cmd output parsing:** only newlines are stripped, so output with trailing spaces or `\r\n` fails on every read.
- **`VirtualSensor`** is dead code and doesn't implement `Sensor`.
- **The polling rate applies to all sensors.** A slow cmd or disk sensor can't be polled less often than a fast
  hwmon sensor.
