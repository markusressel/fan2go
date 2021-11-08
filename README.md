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
nvme-pci-0400
 Sensors   Index   Label       Name          Value  
           1       Composite   temp1_input   53850  
           2       Sensor 1    temp2_input   53850  
           3       Sensor 2    temp3_input   56850  

nvme-pci-0500
 Sensors   Index   Label       Name          Value  
           1       Composite   temp1_input   28850  
           2       Sensor 1    temp2_input   28850  
           3       Sensor 2    temp3_input   42850  

nvme-pci-0100
 Sensors   Index   Label       Name          Value  
           1       Composite   temp1_input   55850  
           2       Sensor 1    temp2_input   55850  
           3       Sensor 2    temp3_input   57850  

nct6798
 Fans      Index   Label    Name   RPM    PWM   Auto   
           1       hwmon4   pwm1   0      153   false  
           2       hwmon4   pwm2   1223   104   false  
           3       hwmon4   pwm3   677    107   false  
           4       hwmon4   pwm4   658    106   false  
           5       hwmon4   pwm5   663    107   false  
           6       hwmon4   pwm6   0      255   false  
           7       hwmon4   pwm7   0      255   false  
 Sensors   Index   Label                      Name          Value   
           1       SYSTIN                     temp1_input   41000   
           2       CPUTIN                     temp2_input   64000   
           3       AUXTIN0                    temp3_input   22000   
           4       AUXTIN1                    temp4_input   127000  
           5       AUXTIN2                    temp5_input   98000   
           6       AUXTIN3                    temp6_input   32000   
           7       PECI Agent 0 Calibration   temp7_input   71500   
           8       PCH_CHIP_CPU_MAX_TEMP      temp8_input   0       
           9       PCH_CHIP_TEMP              temp9_input   0       

k10temp-pci-00183
 Sensors   Index   Label   Name          Value  
           1       Tctl    temp1_input   82250  
           2       Tdie    temp2_input   82250  
           3       Tccd1   temp3_input   67250  

iwlwifi_1
 Sensors   Index   Label    Name          Value  
           1       hwmon7   temp1_input   43000  

amdgpu-pci-0031
 Fans      Index   Label    Name   RPM   PWM   Auto   
           1       hwmon8   pwm1   561   43    false  
 Sensors   Index   Label      Name          Value  
           1       edge       temp1_input   58000  
           2       junction   temp2_input   61000  
           3       mem        temp3_input   56000  
```

Then configure fan2go by creating a YAML configuration file in **one** of the following locations:

* `./fan2go.yaml`
* `/etc/fan2go/fan2go.yaml` (recommended)
* `/root/.fan2go/fan2go.yaml`

```shell
sudo mkdir /etc/fan2go
```

#### Example

An example configuration file including documentation can be found in [fan2go.yaml](/fan2go.yaml).

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
