# fan2go

A daemon to control the fans of a computer.

![graph](screenshots/graph.png)

## How to use

fan2go relies on [lm-sensors](https://github.com/lm-sensors/lm-sensors) to get both temperature and RPM sensor readings,
as well as PWM controls, so you will have
to [set it up first](https://wiki.archlinux.org/index.php/Lm_sensors#Installation).

### Installation

#### AUR

A third-party maintained AUR package has been created by [manvari](https://github.com/manvari).

```shell
yay -S fan2go-git
```

#### Manual

Download the latest release from GitHub:

```shell
curl -L -o fan2go https://github.com/markusressel/fan2go/releases/latest/download/fan2go-linux-amd64
chmod +x fan2go
sudo cp ./fan2go /usr/bin/fan2go
fan2go -h
```

### Configuration

Then configure fan2go by creating a YAML configuration file in **one** of the following locations:

* `./fan2go.yaml`
* `/etc/fan2go/fan2go.yaml` (recommended)
* `/root/.fan2go/fan2go.yaml`

```shell
sudo mkdir /etc/fan2go
sudo nano /etc/fan2go/fan2go.yaml
```

The most important configuration options you need to define are the `fans:`, `sensors:` and `curves:` sections.

#### Fans

Under `fans:` you need to define a list of fan devices that you want to control using fan2go. To detect fans on your
system run `fan2go detect`, which will print a list of devices exposed by the hwmon filesystem backend:

```shell
> fan2go detect
nct6798
 Fans      Index   Label    Name   RPM    PWM   Auto   
           1       hwmon4   pwm1   0      153   false  
           2       hwmon4   pwm2   1223   104   false  
           3       hwmon4   pwm3   677    107   false  
 Sensors   Index   Label    Name          Value   
           1       SYSTIN   temp1_input   41000   
           2       CPUTIN   temp2_input   64000   
 
amdgpu-pci-0031
 Fans      Index   Label    Name   RPM   PWM   Auto   
           1       hwmon8   pwm1   561   43    false  
 Sensors   Index   Label      Name          Value  
           1       edge       temp1_input   58000  
           2       junction   temp2_input   61000  
           3       mem        temp3_input   56000  
```

To use detected devices in your configuration, use the `hwmon` fan type:

```yaml
# A list of fans to control
fans:
  # A user defined ID.
  # Used for logging only
  - id: cpu
    # The type of fan configuration
    type: hwmon
    params:
      # The platform of the controller which is
      # connected to this fan (see sensor.platform above)
      platform: cpu
      # The index of this fan as displayed by `sensors`
      index: 1
    # Indicates whether this fan should never stop rotating, regardless of
    # how low the curve value is
    neverStop: yes
    # The curve ID that should be used to determine the
    # speed of this fan
    curve: cpu_curve
```

#### Sensors

Under `sensors:` you need to define a list of temperature sensor devices that you want to monitor and use to adjust
fanspeeds. Like with fans, you can find usable devices using `fan2go detect`.

```yaml
# A list of sensors to monitor
sensors:
  # A user defined ID, which is used to reference
  # a sensor in a curve configuration (see below)
  - id: cpu_package
    # The type of sensor configuration
    type: hwmon
    params:
      # The controller platform as displayed by `fan2go detect`, f.ex.:
      # "nouveau", "coretemp" or "it8620" etc.
      platform: coretemp
      # The index of this sensor as displayed by `fan2go detect`
      index: 1
```

#### Curves

Under `curves:` you need to define a list of fan speed curves, which represent the speed of a fan based on one or more
temperature sensors.

##### Linear

To create a simple, linear speed curve, use a curve of type `linear`.

This curve type can be used with a min/max sensor value, where the min temp will result in a curve value of 0 and the
max temp will result in a curve value of 100:

```yaml
curves:
  - id: cpu_curve
    # The type of the curve
    type: linear
    # Parameters needed for a specific curve type.
    params:
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
    type: linear
    # Parameters needed for the given curve type.
    params:
      # The sensor ID to use as a temperature input
      sensor: cpu_package
      # Steps to define a section-wise defined speed curve function.
      steps:
        # Sensor value -> Speed (in percent)
        - 40: 0
        - 50: 30
        - 80: 100
```

##### Function

To create more complex curves you can combine exising curves using a curve of type `function`:

```yaml
curves:
  - id: case_avg_curve
    type: function
    params:
      # Type of aggregation function to use, on of: minimum | maximum | average
      function: average
      # A list of curve IDs to use
      curves:
        - cpu_curve
        - mainboard_curve
        - ssd_curve
```

#### Example

An example configuration file including more detailed documentation can be found in [fan2go.yaml](/fan2go.yaml).

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
ExecStart=/usr/bin/fan2go -c /etc/fan2go/fan2go.yaml --no-style
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
of all sensors specified in the config. Sensor values are stored as a moving average of size `rollingWindowSize` (
see [configuration](#configuration)).

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
