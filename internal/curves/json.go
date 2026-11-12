package curves

import (
	"encoding/json"

	"github.com/markusressel/fan2go/internal/configuration"
)

// The fan controllers evaluate the curves while the REST API serializes them.
// The MarshalJSON methods below serialize a snapshot of the curve that is taken under its lock.
// Each snapshot type has the same fields and json tags as the curve type, without the mutex.

type curveJson struct {
	Config configuration.CurveConfig `json:"config"`
	Value  float64                   `json:"value"`
}

func (c *LinearSpeedCurve) MarshalJSON() ([]byte, error) {
	c.mu.RLock()
	snapshot := curveJson{Config: c.Config, Value: c.Value}
	c.mu.RUnlock()
	return json.Marshal(snapshot)
}

func (c *PidSpeedCurve) MarshalJSON() ([]byte, error) {
	c.mu.RLock()
	snapshot := curveJson{Config: c.Config, Value: c.Value}
	c.mu.RUnlock()
	return json.Marshal(snapshot)
}

func (c *FunctionSpeedCurve) MarshalJSON() ([]byte, error) {
	c.mu.RLock()
	snapshot := curveJson{Config: c.Config, Value: c.Value}
	c.mu.RUnlock()
	return json.Marshal(snapshot)
}

type staircaseCurveJson struct {
	Config   configuration.CurveConfig `json:"config"`
	Value    float64                   `json:"value"`
	LastTemp int
}

func (c *StaircaseSpeedCurve) MarshalJSON() ([]byte, error) {
	c.mu.RLock()
	snapshot := staircaseCurveJson{Config: c.Config, Value: c.Value, LastTemp: c.LastTemp}
	c.mu.RUnlock()
	return json.Marshal(snapshot)
}
