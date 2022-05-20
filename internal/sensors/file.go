package sensors

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"os/user"
	"path/filepath"
	"strings"
)

type FileSensor struct {
	Name      string                     `json:"name"`
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`
}

func (sensor FileSensor) GetId() string {
	return sensor.Config.ID
}

func (sensor FileSensor) GetConfig() configuration.SensorConfig {
	return sensor.Config
}

func (sensor FileSensor) GetValue() (float64, error) {
	filePath := sensor.Config.File.Path
	// resolve home dir path
	if strings.HasPrefix(filePath, "~") {
		currentUser, err := user.Current()
		if err != nil {
			return 0, err
		}

		filePath = filepath.Join(currentUser.HomeDir, filePath[1:])
	}

	integer, err := util.ReadIntFromFile(filePath)
	if err != nil {
		ui.Warning("Unable to read int from file sensor: %s", filePath)
		return 0, nil
	}

	result := float64(integer)
	return result, nil
}

func (sensor FileSensor) GetMovingAvg() (avg float64) {
	return sensor.MovingAvg
}

func (sensor *FileSensor) SetMovingAvg(avg float64) {
	sensor.MovingAvg = avg
}
