package curves

import (
	"fmt"
	"math"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
)

type SpeedCurve interface {
	GetId() string
	// Evaluate update the value of this SpeedCurve, by calculates a new value based on the current sensor values
	// returns a value in [0..255]
	Evaluate() (value float64, err error)
	// CurrentValue returns the current value of the curve, which was calculated by the previous call to Evaluate
	CurrentValue() float64
}

type RegistryReader interface {
	GetSensor(id string) (sensors.Sensor, bool)
	GetCurve(id string) (SpeedCurve, bool)
}

func NewSpeedCurve(config configuration.CurveConfig) (SpeedCurve, error) {
	if config.Linear != nil {
		ret := &LinearSpeedCurve{
			Config: config,
		}
		return ret, nil
	}

	if config.Staircase != nil {
		ret := &StaircaseSpeedCurve{
			Config:   config,
			LastTemp: math.MinInt,
		}
		return ret, nil
	}

	if config.PID != nil {
		pidLoop := util.NewPidLoop(
			config.PID.P,
			config.PID.I,
			config.PID.D,
			0,
			255,
			true,
			false,
		)
		return &PidSpeedCurve{
			Config:  config,
			pidLoop: pidLoop,
		}, nil
	}

	if config.Function != nil {
		return &FunctionSpeedCurve{
			Config: config,
		}, nil
	}

	return nil, fmt.Errorf("no matching curve type for curve: %s", config.ID)
}
