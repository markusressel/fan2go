package curves

import (
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newLinearHysteresisCurve creates a linear curve 40..80 °C -> 0..255 (6.375 per degree) with the given
// hysteresis.down, and the sensor it reads.
func newLinearHysteresisCurve(t *testing.T, down float64, withSteps bool) (SpeedCurve, *MockSensor) {
	t.Helper()
	s := &MockSensor{Name: "sensor"}
	reg := NewMockRegistry()
	reg.RegisterSensor(s)
	config := createLinearCurveConfig("curve", s.GetId(), 40, 80)
	if withSteps {
		config = createLinearCurveConfigWithSteps("curve", s.GetId(), map[int]float64{40: 0, 80: 255})
	}
	if down != 0 {
		config.Linear.Hysteresis = &configuration.LinearHysteresisConfig{Down: down}
	}
	curve, err := NewSpeedCurve(config)
	require.NoError(t, err)
	reg.RegisterCurve(curve)
	return curve, s
}

// evaluateAt evaluates the curve at the given temperature in degrees.
func evaluateAt(t *testing.T, curve SpeedCurve, s *MockSensor, temp float64) float64 {
	t.Helper()
	s.MovingAvg = temp * 1000
	value, err := curve.Evaluate()
	require.NoError(t, err)
	return value
}

const perDegree = 255.0 / 40

func TestLinearCurve_Hysteresis_OffByDefault(t *testing.T) {
	curve, s := newLinearHysteresisCurve(t, 0, false)

	assert.Equal(t, 20*perDegree, evaluateAt(t, curve, s, 60))
	assert.InDelta(t, 19.5*perDegree, evaluateAt(t, curve, s, 59.5), 1e-9)
}

func TestLinearCurve_Hysteresis_Down(t *testing.T) {
	for _, withSteps := range []bool{false, true} {
		name := "min/max"
		if withSteps {
			name = "steps"
		}
		t.Run(name, func(t *testing.T) {
			// GIVEN: a hysteresis of 3 degrees
			curve, s := newLinearHysteresisCurve(t, 3, withSteps)

			// THEN: a rising temperature is followed at once
			assert.InDelta(t, 10*perDegree, evaluateAt(t, curve, s, 50), 1e-9)
			assert.InDelta(t, 20*perDegree, evaluateAt(t, curve, s, 60), 1e-9)

			// THEN: a drop of up to 3 degrees is ignored, including exactly 3 degrees
			assert.InDelta(t, 20*perDegree, evaluateAt(t, curve, s, 59), 1e-9)
			assert.InDelta(t, 20*perDegree, evaluateAt(t, curve, s, 57.5), 1e-9)
			assert.InDelta(t, 20*perDegree, evaluateAt(t, curve, s, 57), 1e-9)

			// THEN: beyond it, the curve follows the temperature 3 degrees later, continuously
			assert.InDelta(t, 19.9*perDegree, evaluateAt(t, curve, s, 56.9), 1e-9)
			assert.InDelta(t, 15*perDegree, evaluateAt(t, curve, s, 52), 1e-9)

			// THEN: rising again within 3 degrees of the held temperature keeps the value
			assert.InDelta(t, 15*perDegree, evaluateAt(t, curve, s, 54), 1e-9)
			assert.InDelta(t, 15*perDegree, evaluateAt(t, curve, s, 55), 1e-9)
			// above it, the temperature is followed at once
			assert.InDelta(t, 15.5*perDegree, evaluateAt(t, curve, s, 55.5), 1e-9)

			// THEN: the ends of the curve are reached
			assert.Equal(t, 255.0, evaluateAt(t, curve, s, 90))
			assert.Equal(t, 255.0, evaluateAt(t, curve, s, 78))
			assert.Equal(t, 0.0, evaluateAt(t, curve, s, 30))
		})
	}
}

func TestLinearCurve_Hysteresis_UsesFractionalDegrees(t *testing.T) {
	// GIVEN: a hysteresis of 0.5 degrees (the staircase curve truncates the temperature to whole degrees)
	curve, s := newLinearHysteresisCurve(t, 0.5, false)
	evaluateAt(t, curve, s, 60.9)

	// THEN
	assert.InDelta(t, 20.9*perDegree, evaluateAt(t, curve, s, 60.45), 1e-9)
	assert.InDelta(t, 20.8*perDegree, evaluateAt(t, curve, s, 60.3), 1e-9)
}

func TestLinearCurve_Hysteresis_FirstEvaluationUsesTheMeasuredTemperature(t *testing.T) {
	curve, s := newLinearHysteresisCurve(t, 5, false)

	assert.InDelta(t, 10*perDegree, evaluateAt(t, curve, s, 50), 1e-9)
}

func TestLinearCurve_Hysteresis_KeptAcrossAFailedEvaluation(t *testing.T) {
	// GIVEN
	curve, s := newLinearHysteresisCurve(t, 3, false)
	evaluateAt(t, curve, s, 60)
	reg := NewMockRegistry()
	curve.(*LinearSpeedCurve).BindRegistry(reg)

	// WHEN: the sensor can't be read once
	_, err := curve.Evaluate()
	require.Error(t, err)
	reg.RegisterSensor(s)
	curve.(*LinearSpeedCurve).BindRegistry(reg)

	// THEN: the hysteresis continues from before the failure
	assert.InDelta(t, 20*perDegree, evaluateAt(t, curve, s, 58), 1e-9)
}
