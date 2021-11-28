package sensors

import (
	"github.com/markusressel/fan2go/internal/configuration"
)

func CreateSensor(
	id string,
	hwMonConfig configuration.HwMonSensorConfig,
	avgTmp float64,
) (sensor Sensor) {
	sensor = &HwmonSensor{
		Config: configuration.SensorConfig{
			ID:    id,
			HwMon: &hwMonConfig,
		},
		MovingAvg: avgTmp,
	}
	SensorMap[sensor.GetId()] = sensor
	return sensor
}
