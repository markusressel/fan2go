package sensors

import (
	"fmt"
	"sync"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
)

type AcpiSensor struct {
	Config    configuration.SensorConfig `json:"configuration"`
	MovingAvg float64                    `json:"movingAvg"`
	mu        sync.Mutex
}

func (s *AcpiSensor) GetId() string {
	return s.Config.ID
}

func (s *AcpiSensor) GetLabel() string {
	return "ACPI Sensor " + s.Config.ID
}

func (s *AcpiSensor) GetConfig() configuration.SensorConfig {
	return s.Config
}

func (s *AcpiSensor) GetValue() (float64, error) {
	return s.getValueAt(util.ExecuteAcpiCall)
}

func (s *AcpiSensor) getValueAt(callFn func(method, args string) (int64, error)) (float64, error) {
	conf := s.Config.Acpi
	val, err := callFn(conf.Method, conf.Args)
	if err != nil {
		return 0, fmt.Errorf("sensor %s: %w", s.GetId(), err)
	}

	switch conf.Conversion {
	case configuration.AcpiSensorConversionMillicelsius, configuration.AcpiSensorConversionRaw:
		return float64(val), nil
	default:
		// Default: celsius → multiply by 1000 to get millidegrees
		return float64(val) * 1000, nil
	}
}

func (s *AcpiSensor) GetMovingAvg() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MovingAvg
}

func (s *AcpiSensor) SetMovingAvg(avg float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.MovingAvg = avg
}
