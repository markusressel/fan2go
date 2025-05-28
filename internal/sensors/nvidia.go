package sensors

import (
	"errors"
	"sync"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/markusressel/fan2go/internal/configuration"
)

type NvidiaSensor struct {
	Label     string                     `json:"label"`
	Index     int                        `json:"index"` // TODO: needed? there only is one temperature sensor per Device
	Max       int                        `json:"max"`
	Min       int                        `json:"min"`
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`

	// TODO: put nvml.Device here? though in reality it should be shared
	//   between all fans and sensors of that device (but multiple devices can exist
	//   when the system has multiple GPUs)

	mu sync.Mutex
}

func (sensor *NvidiaSensor) GetId() string {
	return sensor.Config.ID
}

func (sensor *NvidiaSensor) GetConfig() configuration.SensorConfig {
	return sensor.Config
}

func (sensor *NvidiaSensor) GetValue() (result float64, err error) {
	var device nvml.Device = nil // TODO!
	tempDegC, ret := device.GetTemperature(nvml.TEMPERATURE_GPU)
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
