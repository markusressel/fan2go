//go:build !disable_nvml

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

	device         nvml.Device
	nvidiaSensorId nvml.TemperatureSensors

	mu sync.Mutex
}

func CreateNvidiaSensor(config configuration.SensorConfig) (Sensor, error) {
	ret := &NvidiaSensor{
		Index:  config.Nvidia.Index,
		Config: config,

		mu: sync.Mutex{},
	}
	err := ret.Init()
	// if the nvidia device can't be found or its temperature sensor can't be read,
	// return error instead of an unusable sensor
	if err != nil {
		return nil, err
	}
	return ret, nil
}

func (sensor *NvidiaSensor) Init() error {
	sensor.device, _ = nvidia_base.GetDevice(sensor.Config.Nvidia.Device)
	if sensor.device == nil {
		return fmt.Errorf("couldn't get handle for nvidia device %s - does it exist?", sensor.Config.Nvidia.Device)
	}
	// if nvml ever supports more than one sensor, map from sensor.Index
	// to the corresponding nvml.TEMPERATURE_* constant here
	sensor.nvidiaSensorId = nvml.TEMPERATURE_GPU

	_, ret := sensor.device.GetTemperature(sensor.nvidiaSensorId)
	if ret != nvml.SUCCESS {
		return fmt.Errorf("apparently nvidia device %s doesn't support reading the temperature, error was: %s",
			sensor.Config.Nvidia.Device, nvml.ErrorString(ret))
	}
	return nil
}

func (sensor *NvidiaSensor) GetId() string {
	return sensor.Config.ID
}

func (sensor *NvidiaSensor) GetLabel() string {
	return sensor.Label
}

func (sensor *NvidiaSensor) GetConfig() configuration.SensorConfig {
	return sensor.Config
}

func (sensor *NvidiaSensor) GetValue() (result float64, err error) {
	tempDegC, ret := sensor.device.GetTemperature(sensor.nvidiaSensorId)
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
