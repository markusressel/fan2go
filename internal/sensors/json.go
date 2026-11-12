package sensors

import (
	"encoding/json"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
)

// The sensor monitors update the moving average of a sensor while the REST API serializes it.
// The MarshalJSON methods below serialize a snapshot of the sensor that is taken under its lock.
// Each snapshot type has the same fields and json tags as the sensor type, without the mutex,
// plus the reading status of the sensor (readingStatusJson).

// readingStatusJson is the reading status of a sensor in the REST API, embedded in every sensor snapshot.
type readingStatusJson struct {
	// LastReadingTime is the time of the last successful reading, omitted until there is one
	LastReadingTime time.Time `json:"lastReadingTime,omitzero"`
	// ConsecutiveFailures is the number of failed readings since the last successful one
	ConsecutiveFailures int `json:"consecutiveFailures"`
	// ReadFailures is the number of failed readings since the sensor was created (start or config reload)
	ReadFailures int `json:"readFailures"`
	// Stale is true while the curves don't accept the value of the sensor (see FreshReading)
	Stale bool `json:"stale"`
}

func newReadingStatusJson(status ReadingStatus) readingStatusJson {
	snapshot := readingStatusJson{
		ConsecutiveFailures: status.ConsecutiveFailures,
		ReadFailures:        status.TotalFailures,
		Stale:               status.IsStale(util.RealClock.Now(), configuration.CurrentConfig.GetSensorStaleTimeout()),
	}
	if status.HasReading {
		snapshot.LastReadingTime = status.LastReading.Time
	}
	return snapshot
}

type hwmonSensorJson struct {
	Label     string                     `json:"label"`
	Index     int                        `json:"index"`
	Channel   int                        `json:"channel"`
	Input     string                     `json:"input"`
	Max       int                        `json:"max"`
	Min       int                        `json:"min"`
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`

	readingStatusJson
}

func (sensor *HwmonSensor) MarshalJSON() ([]byte, error) {
	status := newReadingStatusJson(sensor.GetReadingStatus())
	sensor.mu.Lock()
	snapshot := hwmonSensorJson{
		Label:     sensor.Label,
		Index:     sensor.Index,
		Channel:   sensor.Channel,
		Input:     sensor.Input,
		Max:       sensor.Max,
		Min:       sensor.Min,
		Config:    sensor.Config,
		MovingAvg: sensor.MovingAvg,

		readingStatusJson: status,
	}
	sensor.mu.Unlock()
	return json.Marshal(snapshot)
}

type fileSensorJson struct {
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`

	readingStatusJson
}

func (sensor *FileSensor) MarshalJSON() ([]byte, error) {
	status := newReadingStatusJson(sensor.GetReadingStatus())
	sensor.mu.Lock()
	snapshot := fileSensorJson{
		Config:    sensor.Config,
		MovingAvg: sensor.MovingAvg,

		readingStatusJson: status,
	}
	sensor.mu.Unlock()
	return json.Marshal(snapshot)
}

type cmdSensorJson struct {
	Name      string                     `json:"name"`
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`

	readingStatusJson
}

func (sensor *CmdSensor) MarshalJSON() ([]byte, error) {
	status := newReadingStatusJson(sensor.GetReadingStatus())
	sensor.mu.Lock()
	snapshot := cmdSensorJson{
		Name:      sensor.Name,
		Config:    sensor.Config,
		MovingAvg: sensor.MovingAvg,

		readingStatusJson: status,
	}
	sensor.mu.Unlock()
	return json.Marshal(snapshot)
}

type diskSensorJson struct {
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`

	readingStatusJson
}

func (s *DiskSensor) MarshalJSON() ([]byte, error) {
	status := newReadingStatusJson(s.GetReadingStatus())
	s.mu.Lock()
	snapshot := diskSensorJson{
		Config:    s.Config,
		MovingAvg: s.MovingAvg,

		readingStatusJson: status,
	}
	s.mu.Unlock()
	return json.Marshal(snapshot)
}
