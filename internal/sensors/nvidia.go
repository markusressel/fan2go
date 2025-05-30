package sensors

import (
	"errors"
	"fmt"
	"sync"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/nvidia_base"
)

type NvidiaSensor struct {
	Label string `json:"label"`
	// Note: Index isn't used, at least currently nvml only supports one temperature sensor
	Index     int                        `json:"index"`
	Max       int                        `json:"max"`
	Min       int                        `json:"min"`
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`

	device nvml.Device

	mu sync.Mutex
}

func (sensor *NvidiaSensor) Init() error {
	sensor.device, _ = nvidia_base.GetDevice(sensor.Config.Nvidia.Device)
	if sensor.device == nil {
		return fmt.Errorf("Couldn't get handle for nvidia device %s - does it exist?", sensor.Config.Nvidia.Device)
	}
	_, ret := sensor.device.GetTemperature(nvml.TEMPERATURE_GPU)
	if ret != nvml.SUCCESS {
		return fmt.Errorf("Apparently nvidia device %s doesn't support reading the temperature, error was: %s", nvml.ErrorString(ret))
	}
	return nil
}

func (sensor *NvidiaSensor) GetId() string {
	return sensor.Config.ID
}

func (sensor *NvidiaSensor) GetConfig() configuration.SensorConfig {
	return sensor.Config
}

func (sensor *NvidiaSensor) GetValue() (result float64, err error) {
	tempDegC, ret := sensor.device.GetTemperature(nvml.TEMPERATURE_GPU)
	if ret != nvml.SUCCESS {
		err = errors.New(nvml.ErrorString(ret))
		return 0, err
	}
	result = float64(tempDegC) * 1000 // convert to millidegrees
	return result, err
}

func (sensor *NvidiaSensor) GetMovingAvg() (avg float64) {
	sensor.mu.Lock()
	defer sensor.mu.Unlock()
	return sensor.MovingAvg
}

func (sensor *NvidiaSensor) SetMovingAvg(avg float64) {
	sensor.mu.Lock()
	defer sensor.mu.Unlock()
	sensor.MovingAvg = avg
}
