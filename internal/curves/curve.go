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
	// Evaluate update the value of this SpeedCurve, by calculates a new value based on the current sensor values
	// returns a value in [0..255]
	Evaluate() (value float64, err error)
	// CurrentValue returns the current value of the curve, which was calculated by the previous call to Evaluate
	CurrentValue() float64
}

var (
	speedCurveMap = cmap.New[SpeedCurve]()
)

func NewSpeedCurve(config configuration.CurveConfig) (SpeedCurve, error) {
	if config.Linear != nil {
		ret := &LinearSpeedCurve{
			Config: config,
		}
		ret.Init()
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
