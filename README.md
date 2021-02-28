# fan2go

A daemon to control the fans of a computer.

![graph](screenshots/graph.png)

## How to use

fan2go relies on [lm-sensors](https://github.com/lm-sensors/lm-sensors) to both get temperature and RPM sensor readings,
as well as PWM controls, so you will have
to [set it up first](https://wiki.archlinux.org/index.php/Lm_sensors#Installation).

Download the latest release from GitHub:

```shell
curl -L -o fan2go https://github.com/markusressel/fan2go/releases/latest/download/fan2go-linux-amd64
chmod +x fan2go
sudo cp ./fan2go /usr/bin/fan2go
fan2go -h
```

### Configuration

Use `fan2go detect` to print a list of all usable devices:

```shell
> fan2go detect
Detected Devices:
acpitz
  temp1_input (1): 27800
  temp2_input (2): 29800
nvme
  temp1_input (1): 52850
  temp2_input (2): 52850
  temp3_input (3): 64850
coretemp
  temp1_input (1): 59000
  temp2_input (2): 57000
  temp3_input (3): 52000
  temp4_input (4): 56000
  temp5_input (5): 50000
it8620
  pwm1 (1): RPM: 0 PWM: 70 Auto: false
  pwm2 (2): RPM: 0 PWM: 70 Auto: false
  pwm3 (3): RPM: 709 PWM: 106 Auto: false
  pwm4 (4): RPM: 627 PWM: 94 Auto: false
  pwm5 (5): RPM: 684 PWM: 100 Auto: false
  temp1_input (1): 29000
  temp2_input (2): 32000
  temp3_input (3): 49000
  temp4_input (4): 29000
  temp5_input (5): 46000
  temp6_input (6): 46000
nouveau
  pwm1 (1): RPM: 1560 PWM: 31 Auto: false
  temp1_input (1): 33000
```

Then configure fan2go by creating a YAML configuration file in **one** of the following locations:

* `./fan2go.yaml`
* `/etc/fan2go/fan2go.yaml` (recommended)
* `/root/.fan2go/fan2go.yaml`

```shell
sudo mkdir /etc/fan2go
```

```yaml
# The path of the database file.
dbPath: "/etc/fan2go/fan2go.db"
# The rate to poll temperature sensors at.
tempSensorPollingRate: 200ms
# The number of sensor items to keep in a rolling window array.
rollingWindowSize: 100
# The rate to poll fan RPM input sensors at.
rpmPollingRate: 1s
# Time to wait before increasing the (initially measured) startPWM of a fan
increaseStartPwmAfter: 10s
# The rate to update fan speed targets at.
controllerAdjustmentTickRate: 200ms

# A list of sensors to monitor.
sensors:
  # A user defined ID, which is used to reference a
  # a sensor in a fan configuration (see below)
  - id: cpu_package
    # The controller platform as displayed by `fan2go detect`, f.ex.:
    # "nouveau", "coretemp" or "it8620" etc.
    platform: coretemp
    # The index of this sensor as displayed by `fan2go detect`.
    index: 1
    # The minimum target temp for this sensor.
    # If the sensor falls below this value, all fans referencing it
    # will run at minimum PWM value.
    min: 50
    # The maximum target temp for this sensor.
    # If the sensor is above this value, all fans referencing it
    # will run at maximum PWM value.
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

# A list of fans to control.
fans:
  # An user defined ID.
  # Used for logging only.
  - id: in_front
    # The platform of the controller which is
    # connected to this fan (see sensor.platform above).
    platform: it8620
    # The index of this fan as displayed by `fan2go detect`.
    fan: 3 # HDD Cage (Front)
    # Indicates whether this fan is allowed to fully stop.
    neverStop: no
    # The sensor ID (defined above) that should be used to determine the
    # speed of this fan.
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
sudo fan2go
```

### As a Service

#### Systemd

```
sudo tee /usr/lib/systemd/system/fan2go.service <<- 'EOF'
[Unit]
Description=Advanced Fan Control program
After=lm-sensors.service

[Service]
LimitNOFILE=8192
ExecStart=/usr/bin/fan2go
Restart=always
RestartSec=1s

[Install]
WantedBy=multi-user.target
EOF
```

```shell
sudo systemctl daemon-reload
sudo systemctl enable --now fan2go
# follow logs
journalctl -u fan2go -f
```

## How it works

### Device detection

fan2go scans the `/sys/class/hwmon` directory for hardware monitor paths. All of these paths are then scanned for

- `tempX_input`
- `pwmX_input`
- `pwmX`

files, which represent temperature sensors, RPM sensors and PWM outputs.

### Initialization

When a fan is added to the configuration that fan2go has not seen before, its fan curve will first be analyzed before it
is controlled properly. This means

* spinning down the fans to 0
* slowly ramping up the speed and monitoring RPM changes along the way

**Note that this takes approx. 8 1/2 minutes**, since we have to wait for the fan speed to settle before taking
measurements. Measurements taken during this process will then be used to determine the lowest PWM value at which the
fan is still running, as well as the highest PWM value that still yields a change in RPM.

All of this is saved to a local database, so it is only needed once per fan configuration.

### Monitoring

To monitor changes in temperature sensor values, a goroutine is started which continuously reads the `tempX_input` files
of all sensors specified in the config. Sensor values are stored in a moving window of size `rollingWindowSize` (see
configuration).

### Fan Controllers

To update the fan speed, one goroutine is started **per fan**, which continuously adjusts the PWM value of a given fan
based on the sensor data measured by the monitor. This means:

* calculating the average temperature per sensor using the rolling window data
* calculating the ratio between the average temp and the max/min values defined in the config
* calculating the target PWM of a fan using the previous ratio, taking its startPWM and maxPWM into account
* applying the calculated target PWM to the fan

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
