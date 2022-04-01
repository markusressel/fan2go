package sensors

import (
	"context"
	"errors"
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"os/exec"
	"strconv"
	"strings"
	"time"
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
	if _, err := util.CheckFilePermissionsForExecution(sensor.Exec); err != nil {
		return 0, errors.New(fmt.Sprintf("Sensor %s: Cannot execute %s: %s", sensor.Config.ID, sensor.Exec, err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, sensor.Exec, sensor.Args...)
	out, err := cmd.Output()

	if ctx.Err() == context.DeadlineExceeded {
		ui.Warning("Sensor %s: Command timed out: %s", sensor.Config.ID, sensor.Exec)
		return 0, err
	}

	if err != nil {
		ui.Warning("Sensor %s: Command failed to execute: %s", sensor.Config.ID, sensor.Exec)
		return 0, err
	}

	strout := string(out)
	strout = strings.Trim(strout, "\n")

	temp, err := strconv.ParseFloat(strout, 64)
	if err != nil {
		ui.Warning("Sensor %s: Unable to read int from command output: %s", sensor.Config.ID, sensor.Exec)
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
