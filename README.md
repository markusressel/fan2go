# fan2go

A daemon to control the fans of a computer.

## How to use

Download the latest release from GitHub:

```shell
curl -L -o fan2go  https://github.com/markusressel/fan2go/releases/latest/download/fan2go-linux-amd64
```

### Configuration

```yaml
dbPath: "~/.fan2go.db"
tempSensorPollingRate: 200ms
rollingWindowSize: 100
rpmPollingRate: 1s
controllerAdjustmentTickRate: 200ms
sensors:
  - id: cpu_package
    platform: coretemp
    index: 1
    min: 50
    max: 75
  - id: mainboard
    platform: it8620
    index: 3 # Intel PECI
    min: 50
    max: 80
  - id: sata_ssd
    platform: acpitz
    index: 1
    min: 30
    max: 40
fans:
  - id: in_front
    platform: it8620
    fan: 3 # HDD Cage (Front)
    neverStop: yes
    sensor: sata_ssd
  - id: in_bottom
    platform: it8620
    fan: 4 # Power Supply (Bottom)
    neverStop: yes
    sensor: mainboard
  - id: in_top_double
    platform: it8620
    fan: 5 # Radiator (Top)
    neverStop: yes
    sensor: cpu_package
```

### Run

```shell
./fan2go
```

## How it works

### Device detection

fan2go scans the `/sys/class/hwmon` directory for hardware monitor paths. All of these paths are then scanned for

- `tempX_input`
- `pwmX`
- `pwm_input`

files, which represent temperature sensors, RPM sensors and PWM outputs.

### Initialization

When a fan is added to the configuration that fan2go has not seen before, its fan curve will first be analyzed before it
is controlled properly. This means

* spinning down the fans to 0
* slowly ramping up the speed and monitoring RPM changes along the way

Measurements taken during this process will then be used to determine the lowest PWM value at which the fan is still
running, as well as the highest PWM value that still yields a change in RPM.

All of this is saved to a local bolt database.

### Monitoring

To monitor changes in temperature sensor values, a goroutine is started which continuously reads the `tempX_input` files
of all sensors specified in the config. Sensor values are stored in a moving window of size `rollingWindowSize` (see
configuration).

### Fan Controllers

To update the fan speed, one goroutine is started **per fan**, which continuously adjusts the PWM value of a given fan
based on the sensor data measured by the monitor.

# Dependencies

See [go.mod](go.mod)

# License

```
fan2go
Copyright (C) 2021  Markus Ressel

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published
by the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
```