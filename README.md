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
nvme
  1: Composite (temp1_input): 51850
  2: Sensor 1 (temp2_input): 51850
  3: Sensor 2 (temp3_input): 52850
nvme
  1: Composite (temp1_input): 52850
  2: Sensor 1 (temp2_input): 52850
  3: Sensor 2 (temp3_input): 45850
nvme
  1: Composite (temp1_input): 52850
  2: Sensor 1 (temp2_input): 52850
  3: Sensor 2 (temp3_input): 53850
nct6798
  1: hwmon5 (pwm1): RPM: 0 PWM: 142 Auto: false
  2: hwmon5 (pwm2): RPM: 994 PWM: 68 Auto: false
  3: hwmon5 (pwm3): RPM: 579 PWM: 96 Auto: false
  4: hwmon5 (pwm4): RPM: 345 PWM: 58 Auto: false
  5: hwmon5 (pwm5): RPM: 343 PWM: 57 Auto: false
  6: hwmon5 (pwm6): RPM: 0 PWM: 255 Auto: false
  7: hwmon5 (pwm7): RPM: 0 PWM: 255 Auto: false
  1: SYSTIN (temp1_input): 43000
  2: CPUTIN (temp2_input): 55500
  3: AUXTIN0 (temp3_input): 22000
  4: AUXTIN1 (temp4_input): 127000
  5: AUXTIN2 (temp5_input): 100000
  6: AUXTIN3 (temp6_input): 32000
  7: PECI Agent 0 Calibration (temp7_input): 56500
  8: PCH_CHIP_CPU_MAX_TEMP (temp8_input): 0
  9: PCH_CHIP_TEMP (temp9_input): 0
k10temp
  1: Tctl (temp1_input): 75875
  2: Tdie (temp2_input): 75875
  3: Tccd1 (temp3_input): 69250
iwlwifi_1
  1: hwmon8 (temp1_input): 41000
amdgpu
  1: hwmon9 (pwm1): RPM: 561 PWM: 43 Auto: false
  1: edge (temp1_input): 56000
  2: junction (temp2_input): 59000
  3: mem (temp3_input): 56000
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
rollingWindowSize: 50
# The rate to poll fan RPM input sensors at.
rpmPollingRate: 1s
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

### Print fan curve data

For each newly configured fan **fan2go** measures its fan curve and stores it in a db for future reference. You can take
a look at this measurement using the following command:

```shell
> sudo fan2go curve
nct6798 -> pwm1
                  
 Start PWM   0    
 Max PWM     255  

No fan curve data yet...


nct6798 -> pwm2
                  
 Start PWM   0    
 Max PWM     194  

 1994 ┤                                                                          ╭────────────────────────
 1900 ┤                                                                       ╭──╯
 1805 ┤                                                                  ╭────╯
 1711 ┤                                                             ╭────╯
 1616 ┤                                                        ╭────╯
 1522 ┤                                                    ╭───╯
 1427 ┤                                               ╭────╯
 1333 ┤                                          ╭────╯
 1238 ┤                                    ╭─────╯
 1144 ┤                               ╭────╯
 1049 ┤                         ╭─────╯
  955 ┤                   ╭─────╯
  860 ┤             ╭─────╯
  766 ┤       ╭─────╯
  671 ┤ ╭─────╯
  577 ┼─╯
                                                    RPM / PWM
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
of all sensors specified in the config. Sensor values are stored as a moving average of size `rollingWindowSize` (see
configuration).

### Fan Controllers

To update the fan speed, one goroutine is started **per fan**, which continuously adjusts the PWM value of a given fan
based on the sensor data measured by the monitor. This means:

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
