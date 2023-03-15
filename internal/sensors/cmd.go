package sensors

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"strconv"
	"time"
)

type CmdSensor struct {
	Name      string                     `json:"name"`
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`
}

func (sensor CmdSensor) GetId() string {
	return sensor.Config.ID
}

func (sensor CmdSensor) GetConfig() configuration.SensorConfig {
	return sensor.Config
}

func (sensor CmdSensor) GetValue() (float64, error) {
	timeout := 2 * time.Second
	exec := sensor.Config.Cmd.Exec
	args := sensor.Config.Cmd.Args
	result, err := util.SafeCmdExecution(exec, args, timeout)
	if err != nil {
		return 0, fmt.Errorf("sensor %s: %s", sensor.GetId(), err.Error())
	}

	temp, err := strconv.ParseFloat(result, 64)
	if err != nil {
		ui.Warning("sensor %s: Unable to read int from command output: %s", sensor.GetId(), exec)
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
