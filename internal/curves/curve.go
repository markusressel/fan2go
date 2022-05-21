package curves

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
)

type SpeedCurve interface {
	GetId() string
	// Evaluate calculates the current value of the given curve,
	// returns a value in [0..255]
	Evaluate() (value int, err error)
}

var (
	SpeedCurveMap = map[string]SpeedCurve{}
)

func NewSpeedCurve(config configuration.CurveConfig) (SpeedCurve, error) {
	if config.Linear != nil {
		return &linearSpeedCurve{
			Config: config,
		}, nil
	}

	if config.PID != nil {
		pidLoop := util.NewPidLoop(
			config.PID.P,
			config.PID.I,
			config.PID.D,
		)
		return &pidSpeedCurve{
			Config:  config,
			pidLoop: pidLoop,
		}, nil
	}

	if config.Function != nil {
		return &functionSpeedCurve{
			Config: config,
		}, nil
	}

	return nil, fmt.Errorf("no matching curve type for curve: %s", config.ID)
}
