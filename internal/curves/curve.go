package curves

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/util"
	cmap "github.com/orcaman/concurrent-map/v2"
	"github.com/qdm12/reprint"
)

type SpeedCurve interface {
	GetId() string
	// Evaluate calculates the current value of the given curve,
	// returns a value in [0..255]
	Evaluate() (value int, err error)
}

var (
	speedCurveMap = cmap.New[SpeedCurve]()
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

// RegisterSpeedCurve registers a new speed curve
func RegisterSpeedCurve(curve SpeedCurve) {
	speedCurveMap.Set(curve.GetId(), curve)
}

// GetSpeedCurve returns the speed curve with the given id
func GetSpeedCurve(id string) (SpeedCurve, bool) {
	return speedCurveMap.Get(id)
}

// SnapshotSpeedCurveMap returns a snapshot of the current speed curve map
func SnapshotSpeedCurveMap() map[string]SpeedCurve {
	return reprint.This(speedCurveMap.Items()).(map[string]SpeedCurve)
}
