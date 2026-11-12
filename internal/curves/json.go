package curves

import (
	"encoding/json"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
)

// The curve evaluator evaluates the curves while the REST API serializes them.
// The MarshalJSON methods below serialize a snapshot of the curve that is taken under its lock,
// so the value is the result of the last evaluation cycle.
// Each snapshot type has the same fields and json tags as the curve type, without the mutex,
// plus the error and time of the last evaluation (both omitted until the curve has been evaluated,
// the error also while the evaluation succeeds).

type curveJson struct {
	Config      configuration.CurveConfig `json:"config"`
	Value       float64                   `json:"value"`
	Error       string                    `json:"error,omitempty"`
	EvaluatedAt time.Time                 `json:"evaluatedAt,omitzero"`
}

// newCurveJson returns the snapshot of a curve with the given config, value and last evaluation.
func newCurveJson(config configuration.CurveConfig, value float64, last evaluation) curveJson {
	snapshot := curveJson{Config: config, Value: value, EvaluatedAt: last.at}
	if last.err != nil {
		snapshot.Error = last.err.Error()
	}
	return snapshot
}

func (c *LinearSpeedCurve) MarshalJSON() ([]byte, error) {
	c.mu.RLock()
	snapshot := newCurveJson(c.Config, c.Value, c.last)
	c.mu.RUnlock()
	return json.Marshal(snapshot)
}

func (c *PidSpeedCurve) MarshalJSON() ([]byte, error) {
	c.mu.RLock()
	snapshot := newCurveJson(c.Config, c.Value, c.last)
	c.mu.RUnlock()
	return json.Marshal(snapshot)
}

func (c *FunctionSpeedCurve) MarshalJSON() ([]byte, error) {
	c.mu.RLock()
	snapshot := newCurveJson(c.Config, c.Value, c.last)
	c.mu.RUnlock()
	return json.Marshal(snapshot)
}

type staircaseCurveJson struct {
	Config      configuration.CurveConfig `json:"config"`
	Value       float64                   `json:"value"`
	Error       string                    `json:"error,omitempty"`
	EvaluatedAt time.Time                 `json:"evaluatedAt,omitzero"`
	LastTemp    int
}

func (c *StaircaseSpeedCurve) MarshalJSON() ([]byte, error) {
	c.mu.RLock()
	base := newCurveJson(c.Config, c.Value, c.last)
	snapshot := staircaseCurveJson{
		Config:      base.Config,
		Value:       base.Value,
		Error:       base.Error,
		EvaluatedAt: base.EvaluatedAt,
		LastTemp:    c.LastTemp,
	}
	c.mu.RUnlock()
	return json.Marshal(snapshot)
}
