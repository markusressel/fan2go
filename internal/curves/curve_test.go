package curves

import (
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
)

type MockSensor struct {
	ID        string
	Name      string
	MovingAvg float64
	// ReadingTime returns the time of the last successful reading, whose value is MovingAvg.
	// nil means time.Now, so the reading is always fresh.
	ReadingTime func() time.Time
	// NoReading makes the sensor report that it has no successful reading
	NoReading bool
}

func (sensor MockSensor) GetId() string {
	return sensor.ID
}

func (sensor MockSensor) GetLabel() string {
	return sensor.Name
}

func (sensor MockSensor) GetConfig() configuration.SensorConfig {
	panic("not implemented")
}

func (sensor MockSensor) GetValue() (result float64, err error) {
	return sensor.MovingAvg, nil
}

func (sensor MockSensor) GetMovingAvg() (avg float64) {
	return sensor.MovingAvg
}

func (sensor *MockSensor) SetMovingAvg(avg float64) {
	sensor.MovingAvg = avg
}

func (sensor MockSensor) GetLastReading() (sensors.Reading, bool) {
	if sensor.NoReading {
		return sensors.Reading{}, false
	}
	readingTime := time.Now()
	if sensor.ReadingTime != nil {
		readingTime = sensor.ReadingTime()
	}
	return sensors.Reading{Value: sensor.MovingAvg, Time: readingTime}, true
}

func (sensor MockSensor) RecordReading(r sensors.Reading) int {
	panic("not implemented")
}

func (sensor MockSensor) RecordFailure() int {
	panic("not implemented")
}

func (sensor MockSensor) GetConsecutiveFailures() int {
	return 0
}

func (sensor MockSensor) GetReadingStatus() sensors.ReadingStatus {
	reading, ok := sensor.GetLastReading()
	return sensors.ReadingStatus{LastReading: reading, HasReading: ok}
}
