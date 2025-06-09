//go:build disable_nvml

package sensors

import (
	"errors"

	"github.com/markusressel/fan2go/internal/configuration"
)

func CreateNvidiaSensor(config configuration.SensorConfig) (Sensor, error) {
	return nil, errors.New("This version of fan2go was built without NVIDIA (nvml) support")
}
