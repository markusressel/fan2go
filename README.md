<p align="center">
  <img src="screenshots/fan2go_icon.svg" width="144" height="144" alt="fan2go icon">
</p>

<p align="center">A daemon to control the fans of a computer.</p>

<p align="center"><img src="screenshots/graph.png" width=90% alt="Screenshot of Pyrra"></p>

# How to use

fan2go relies on [lm-sensors](https://github.com/lm-sensors/lm-sensors) to get both temperature and RPM sensor readings,
as well as PWM controls, so you will have
to [set it up first](https://wiki.archlinux.org/index.php/Lm_sensors#Installation).

## Installation

### AUR

A third-party maintained AUR package has been created by [manvari](https://github.com/manvari).

```shell
yay -S fan2go-git
```

### Manual

Download the latest release from GitHub:

```shell
curl -L -o fan2go https://github.com/markusressel/fan2go/releases/latest/download/fan2go-linux-amd64
chmod +x fan2go
sudo cp ./fan2go /usr/bin/fan2go
fan2go -h
```

Or compile yourself:

```shell
git clone https://github.com/markusressel/fan2go.git
cd fan2go
make build
sudo cp ./bin/fan2go /usr/bin/fan2go
sudo chmod ug+x /usr/bin/fan2go
```

## Configuration

Then configure fan2go by creating a YAML configuration file in **one** of the following locations:

* `/etc/fan2go/fan2go.yaml` (recommended)
* `/root/.fan2go/fan2go.yaml`
* `./fan2go.yaml`

```shell
sudo mkdir /etc/fan2go
sudo nano /etc/fan2go/fan2go.yaml
```

The most important configuration options you need to define are the `fans:`, `sensors:` and `curves:` sections.

### Fans

Under `fans:` you need to define a list of fan devices that you want to control using fan2go. To detect fans on your
system run `fan2go detect`, which will print a list of devices exposed by the hwmon filesystem backend:

```shell
> fan2go detect
nct6798
 Fans      Index   Label    RPM    PWM   Auto
           1       hwmon4   0      153   false
           2       hwmon4   1223   104   false
           3       hwmon4   677    107   false
 Sensors   Index   Label    Value
           1       SYSTIN   41000
           2       CPUTIN   64000

amdgpu-pci-0031
 Fans      Index   Label    RPM   PWM   Auto
           1       hwmon8   561   43    false
 Sensors   Index   Label      Value
           1       edge       58000
           2       junction   61000
           3       mem        56000
```

To use detected devices in your configuration, use the `hwmon` fan type:

```yaml
# A list of fans to control
fans:
  # A user defined ID.
  # Used for logging only
  - id: cpu
    # The type of fan configuration, one of: hwmon | file
    hwmon:
      # The platform of the controller which is
      # connected to this fan (see sensor.platform below)
      platform: nct6798
      # The index of this fan as displayed by `fan2go detect`
      index: 1
    # Indicates whether this fan should never stop rotating, regardless of
    # how low the curve value is
    neverStop: yes
    # The curve ID that should be used to determine the
    # speed of this fan
    curve: cpu_curve
```

```yaml
fans:
  - id: file_fan
    file:
      path: /tmp/file_fan
```

```bash
> cat /tmp/file_fan
255
```

### Sensors

Under `sensors:` you need to define a list of temperature sensor devices that you want to monitor and use to adjust
fanspeeds. Like with fans, you can find usable devices using `fan2go detect`.

```yaml
# A list of sensors to monitor
sensors:
  # A user defined ID, which is used to reference
  # a sensor in a curve configuration (see below)
  - id: cpu_package
    # The type of sensor configuration, one of: hwmon | file
    hwmon:
      # A regex matching a controller platform displayed by `fan2go detect`, f.ex.:
      # "coretemp", "it8620", "corsaircpro-*" etc.
      platform: coretemp
      # The index of this sensor as displayed by `fan2go detect`
      index: 1
```

```yaml
sensors:
  - id: file_sensor
    file:
      path: /tmp/file_sensor
```

The file contains a value in milli-units, like milli-degrees.

```bash
> cat /tmp/file_sensor
10000
```

### Curves

Under `curves:` you need to define a list of fan speed curves, which represent the speed of a fan based on one or more
temperature sensors.

#### Linear

To create a simple, linear speed curve, use a curve of type `linear`.

This curve type can be used with a min/max sensor value, where the min temp will result in a curve value of `0` and the
max temp will result in a curve value of `255`:

```yaml
curves:
  - id: cpu_curve
    # The type of the curve, one of: linear | function
    linear:
      # The sensor ID to use as a temperature input
      sensor: cpu_package
      # Sensor input value at which the curve is at minimum speed
      min: 40
      # Sensor input value at which the curve is at maximum speed
      max: 80
```

You can also define the curve in multiple, linear sections using the `steps` parameter:

```yaml
curves:
  - id: cpu_curve
    # The type of the curve
    linear:
      # The sensor ID to use as a temperature input
      sensor: cpu_package
      # Steps to define a section-wise defined speed curve function.
      steps:
        # Sensor value -> Speed (in pwm)
        - 40: 0
        - 50: 50
        - 80: 255
```

#### Function

To create more complex curves you can combine exising curves using a curve of type `function`:

```yaml
curves:
  - id: case_avg_curve
    function:
      # Type of aggregation function to use, one of: minimum | maximum | average | delta
      type: average
      # A list of curve IDs to use
      curves:
        - cpu_curve
        - mainboard_curve
        - ssd_curve
```

### Example

An example configuration file including more detailed documentation can be found in [fan2go.yaml](/fan2go.yaml).

## Run

```shell
sudo fan2go
```

## As a Service

### Systemd

When installing fan2go using a package, it comes with a [systemd unit file](./fan2go.service). To enable it simply run:

```shell
sudo systemctl daemon-reload
sudo systemctl enable --now fan2go
# follow logs
journalctl -u fan2go -f
```

## Print fan curve data

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

## Statistics

fan2go has a prometheus exporter built in, which you can use to extract data over time. Simply enable it in your
configuration and you are good to go:

```yaml
statistics:
  # Whether to enable the prometheus exporter or not
  enabled: true
  # The port to expose the exporter on
  port: 9000
```

You can then see the metics on [http://localhost:9000/metrics](http://localhost:9000/metrics).

# How it works

## Device detection

fan2go uses [gosensors](https://github.com/md14454/gosensors) to directly interact with lm-sensors.

## Initialization

To properly control a fan which fan2go has not seen before, its speed curve is analyzed. This means

* spinning down the fans to 0
* slowly ramping up the speed and monitoring RPM changes along the way

**Note that this takes approx. 8 1/2 minutes**, since we have to wait for the fan speed to settle before taking
measurements. Measurements taken during this process will then be used to determine the lowest PWM value at which the
fan is still running, as well as the highest PWM value that still yields a change in RPM.

All of this is saved to a local database (path given by the `dbPath` config option), so it is only needed once per fan configuration.

To reduce the risk of runnin the whole system on low fan speeds for such a long period of time, you can force fan2go to initialize only
one fan at a time, using the `runFanInitializationInParallel: false` config option.

## Monitoring

Temperature and RPM sensors are polled continuously at the rate specified by the `tempSensorPollingRate` config option.
`tempRollingWindowSize`/`rpmRollingWindowSize` amount of measurements are always averaged and stored as the average sensor value.

## Fan Controllers

Fan speeds are continuously adjusted at the rate specified by the `controllerAdjustmentTickRate` config option based on the value of their associated curve.

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
