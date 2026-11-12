package sensors

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
)

type FileSensor struct {
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`

	ReadingTracker

	mu sync.Mutex
}

func (sensor *FileSensor) GetId() string {
	return sensor.Config.ID
}

func (sensor *FileSensor) GetLabel() string {
	return "File Sensor " + sensor.Config.ID
}

func (sensor *FileSensor) GetConfig() configuration.SensorConfig {
	return sensor.Config
}

func (sensor *FileSensor) GetValue() (float64, error) {
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
		// a failed read must never look like a valid reading (e.g. 0 °C)
		return 0, fmt.Errorf("sensor %s: unable to read int from file %s: %w", sensor.GetId(), filePath, err)
	}

	result := float64(integer)
	return result, nil
}

func (sensor *FileSensor) GetMovingAvg() (avg float64) {
	sensor.mu.Lock()
	defer sensor.mu.Unlock()
	return sensor.MovingAvg
}

func (sensor *FileSensor) SetMovingAvg(avg float64) {
	sensor.mu.Lock()
	defer sensor.mu.Unlock()
	sensor.MovingAvg = avg
}
