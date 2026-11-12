package curves

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type FunctionSpeedCurve struct {
	Config   configuration.CurveConfig `json:"config"`
	Value    float64                   `json:"value"`
	registry RegistryReader
	// clock is the time source for the time of the evaluation (nil means util.RealClock)
	clock util.Clock

	// last is the error and time of the last evaluation (guarded by mu, like Value)
	last evaluation

	mu sync.RWMutex
}

func (c *FunctionSpeedCurve) BindRegistry(registry RegistryReader) {
	c.registry = registry
}

// SetClock replaces the time source for the time of the evaluation. Intended for tests.
func (c *FunctionSpeedCurve) SetClock(clock util.Clock) {
	c.clock = clock
}

func (c *FunctionSpeedCurve) GetId() string {
	return c.Config.ID
}

// dependencies returns the ids of the sub-curves, which must be evaluated before this curve.
func (c *FunctionSpeedCurve) dependencies() []string {
	return c.Config.Function.Curves
}

// Evaluate combines the results of the sub-curves (see CurrentResult). It doesn't evaluate them itself:
// the CurveEvaluator evaluates them before this curve in the same cycle, so a sub-curve that is used by
// more than one curve or fan is only evaluated once per cycle.
// If a sub-curve result has an error (or a sub-curve hasn't been evaluated yet), the evaluation fails
// with that error.
func (c *FunctionSpeedCurve) Evaluate() (value float64, err error) {
	now := getClock(c.clock).Now()
	value, err = c.evaluate()
	return c.storeResult(value, err, now)
}

func (c *FunctionSpeedCurve) evaluate() (value float64, err error) {
	if c.registry == nil {
		return 0, fmt.Errorf("no registry bound to speed curve '%s'", c.Config.ID)
	}
	var curves []SpeedCurve
	for _, curveId := range c.Config.Function.Curves {
		curve, exists := c.registry.GetCurve(curveId)
		if !exists || curve == nil {
			return 0, fmt.Errorf("sub-curve not found with id '%s'", curveId)
		}
		curves = append(curves, curve)
	}

	var values []float64
	for _, curve := range curves {
		result := curve.CurrentResult()
		if result.Err != nil {
			// an error of any sub-curve fails the whole curve (e.g. a maximum over two sensors
			// must not ignore the one that can't be read)
			if errors.Is(result.Err, ErrCurveNotEvaluated) {
				return 0, fmt.Errorf("curve %s: sub-curve %s: %w", c.Config.ID, curve.GetId(), result.Err)
			}
			return 0, fmt.Errorf("curve %s: %w", c.Config.ID, result.Err)
		}
		values = append(values, result.Value)
	}

	switch c.Config.Function.Type {
	case configuration.FunctionSum:
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		value = math.Min(255, sum)
	case configuration.FunctionDifference:
		difference := 0.0
		for idx, v := range values {
			if idx == 0 {
				difference = v
			} else {
				difference -= v
			}
		}
		value = math.Max(0, difference)
	case configuration.FunctionDelta:
		var dmax = values[0]
		var dmin = values[0]
		for _, v := range values {
			dmin = math.Min(dmin, v)
			dmax = math.Max(dmax, v)
		}
		delta := dmax - dmin
		value = delta
	case configuration.FunctionMinimum:
		minimum := util.MinValOrElse(values, values[0])
		value = minimum
	case configuration.FunctionMaximum:
		maximum := util.MaxValOrElse(values, values[0])
		value = maximum
	case configuration.FunctionAverage:
		avg := util.Avg(values)
		value = avg
	default:
		// unreachable after config validation, but must not exit the process on the control path
		return 0, fmt.Errorf("unknown curve function %q in curve '%s'", c.Config.Function.Type, c.Config.ID)
	}

	ui.Debug("Evaluating curve '%s'. Curve values: '%v' Desired speed: %.2f", c.Config.ID, values, value)
	return value, nil
}

func (c *FunctionSpeedCurve) storeResult(value float64, err error, now time.Time) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.Value = value
	}
	c.last = evaluation{err: err, at: now}
	return c.Value, err
}

func (c *FunctionSpeedCurve) SetValue(value float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Value = value
}

func (c *FunctionSpeedCurve) CurrentValue() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Value
}

func (c *FunctionSpeedCurve) CurrentResult() CurveResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.last.result(c.Value)
}
