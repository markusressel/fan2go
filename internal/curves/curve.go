package curves

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
	cmap "github.com/orcaman/concurrent-map/v2"
)

type SpeedCurve interface {
	GetId() string
	// Evaluate calculates the current value of the given curve,
	// returns a value in [0..255]
	Evaluate() (value int, err error)
}

var (
	SpeedCurveMap = cmap.New[SpeedCurve]()
)

func NewSpeedCurve(config configuration.CurveConfig) (SpeedCurve, error) {
	if config.Linear != nil {
		return &LinearSpeedCurve{
			Config: config,
		}, nil
	}

	if config.PID != nil {
		pidLoop := util.NewPidLoop(
			config.PID.P,
			config.PID.I,
			config.PID.D,
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
