package sensors

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"os/exec"
	"strconv"
	"strings"
)

type CmdSensor struct {
	Name      string                     `json:"name"`
	Exec      string                     `json:"exec"`
	Args      []string                   `json:"args"`
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"moving_avg"`
}

func (sensor CmdSensor) GetId() string {
	return sensor.Config.ID
}

func (sensor CmdSensor) GetConfig() configuration.SensorConfig {
	return sensor.Config
}

func (sensor CmdSensor) GetValue() (float64, error) {
	cmd := exec.Command(sensor.Exec, sensor.Args...)

	out, err := cmd.Output()
	if err != nil {
		ui.Warning("Command failed to execute: %s", sensor.Exec)
		return 0, err
	}

	strout := string(out)
	strout = strings.Trim(strout, "\n")

	temp, err := strconv.ParseFloat(strout, 64)
	if err != nil {
		ui.Warning("Unable to read int from command output: %s", sensor.Exec)
		return 0, err
	}

	return temp, nil
}

func (sensor CmdSensor) GetMovingAvg() (avg float64) {
	return sensor.MovingAvg
}

func (sensor *CmdSensor) SetMovingAvg(avg float64) {
	sensor.MovingAvg = avg
}
