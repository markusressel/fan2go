package curves

import (
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
)

// helper function to create a pid curve configuration
func createPidCurveConfig(
	id string,
	sensorId string,
	setPoint float64,
	p float64,
	i float64,
	d float64,
) (curve configuration.CurveConfig) {
	curve = configuration.CurveConfig{
		ID: id,
		PID: &configuration.PidCurveConfig{
			Sensor:   sensorId,
			SetPoint: setPoint,
			P:        p,
			I:        i,
			D:        d,
		},
	}
	return curve
}

const setPoint = 60.0
const testSensorID = "mock_sensor"
const testCurveID = "pid_curve"
const loopDelay = 200 * time.Millisecond // dt between two simulated cycles

// Define test gains suitable for 0-255 output range
const testP = -20.0
const testI = -10.0
const testD = -10.0

// How the PID loop evaluates, which the expectations below rely on:
//   - The first Evaluate only initializes the loop: it returns the clamped P term, without I or D.
//   - Every later Evaluate returns P + I + D, where the I term uses the integral accumulated by
//     the *previous* cycles. The current error is added to the integral only after the output
//     has been calculated, so the I term always lags one cycle behind.
//   - The D term acts on the change of the measurement (not of the error): D = -d * dMeasured/dt.

// setupTest creates a PID curve via NewSpeedCurve, bound to a mock sensor at initialTemp.
// The returned tick function advances the curve's clock by loopDelay, standing in for the time between two cycles.
func setupTest(t *testing.T, p, i, d float64, initialTemp float64) (*PidSpeedCurve, *MockSensor, func()) {
	mockSensor := &MockSensor{
		ID:        testSensorID,
		Name:      "Mock Temp Sensor",
		MovingAvg: initialTemp * 1000, // Store in milli-units
	}
	reg := NewMockRegistry()
	reg.RegisterSensor(mockSensor)

	cfg := createPidCurveConfig(testCurveID, testSensorID, setPoint, p, i, d)
	speedCurve, err := NewSpeedCurve(cfg)
	if err != nil {
		t.Fatalf("Failed to create speed curve: %v", err)
	}
	curve, ok := speedCurve.(*PidSpeedCurve)
	if !ok {
		t.Fatalf("Created curve is not a *PidSpeedCurve")
	}
	reg.RegisterCurve(curve)

	now := time.Unix(0, 0)
	curve.pidLoop.SetClock(func() time.Time { return now })
	tick := func() { now = now.Add(loopDelay) }

	return curve, mockSensor, tick
}

// --- Steady State Tests ---

func TestPidCurve_SteadyState_WayBelow(t *testing.T) {
	curve, _, _ := setupTest(t, testP, testI, testD, 40.0) // Temp 40C << 60C

	result, err := curve.Evaluate()
	assert.NoError(t, err)
	// Error = 60 - 40 = 20. P term = -20 * 20 = -400.
	// Output is negative, clamped to 0 by pidLoop.
	assert.Equal(t, 0.0, result, "Speed should be 0 when way below setpoint")
}

func TestPidCurve_SteadyState_AtSetpoint(t *testing.T) {
	curve, _, tick := setupTest(t, testP, testI, testD, 60.0) // Temp 60C == 60C

	result, err := curve.Evaluate()
	assert.NoError(t, err)
	// Error = 0. P = 0. Output = 0.
	assert.Equal(t, 0.0, result, "Speed should be 0 when at setpoint")

	tick()
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	// Error = 0, so the integral stays 0. Temp unchanged, so D = 0. Output = 0.
	assert.Equal(t, 0.0, result, "Speed should stay 0 when at setpoint")
}

func TestPidCurve_SteadyState_SlightlyAbove(t *testing.T) {
	curve, sensor, tick := setupTest(t, testP, testI, testD, 61.0) // Temp 61C > 60C

	// Step 0 (initialization): Error = 60 - 61 = -1. P term = -20 * -1 = 20. Output = 20.
	result, err := curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 20, result, 0.001, "Step 0 - Speed should be 20 when slightly above setpoint")

	// Step 1: P = 20. The integral is still 0, so I = 0. D = 0. Output = 20.
	// Afterwards, the integral becomes err*dt = -1 * 0.2 = -0.2.
	tick()
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 20, result, 0.001, "Step 1 - The I term lags one cycle, so the speed stays 20")

	// Step 2: P = 20. I term = -10 * -0.2 = 2. D = 0. Output = 22.
	tick()
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 22, result, 0.001, "Step 2 - The I term starts increasing the speed")

	// Check sensor wasn't modified
	assert.Equal(t, 61000.0, sensor.MovingAvg)
}

func TestPidCurve_SteadyState_ModeratelyAbove(t *testing.T) {
	curve, _, tick := setupTest(t, testP, testI, testD, 65.0) // Temp 65C > 60C

	// Step 0 (initialization): Error = 60 - 65 = -5. P term = -20 * -5 = 100. Output = 100.
	result, err := curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 100, result, 0.001, "Step 0 - Speed should be 100 when moderately above setpoint")

	// Step 1: P = 100. The integral is still 0, so I = 0. D = 0. Output = 100.
	// Afterwards, the integral becomes err*dt = -5 * 0.2 = -1.0.
	tick()
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 100, result, 0.001, "Step 1 - The I term lags one cycle, so the speed stays 100")

	// Step 2: P = 100. I term = -10 * -1.0 = 10. D = 0. Output = 110.
	tick()
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 110, result, 0.001, "Step 2 - The I term starts increasing the speed")
}

func TestPidCurve_SteadyState_WayAbove(t *testing.T) {
	curve, _, _ := setupTest(t, testP, testI, testD, 75.0) // Temp 75C >> 60C

	result, err := curve.Evaluate()
	assert.NoError(t, err)
	// Error = 60 - 75 = -15. P term = -20 * -15 = 300.
	// Output clamped to 255 by pidLoop.
	assert.Equal(t, 255.0, result, "Speed should saturate to 255 when way above setpoint")
}

// --- Dynamic Tests ---

func TestPidCurve_IntegralAction_RampUp(t *testing.T) {
	// Using only I term (P=0, D=0). Negative I gain needed for cooling control.
	curve, sensor, tick := setupTest(t, 0, testI, 0, 62.0) // Temp 62C > 60C

	// Error = 60 - 62 = -2. Each cycle adds err*dt = -2 * 0.2 = -0.4 to the integral,
	// which increases the I term by -10 * -0.4 = +4.0.
	// Step 0 is the initialization (P term only = 0). Step 1 uses the still empty integral (0).
	// From step 2 on, the I term grows by 4 per step.
	expectedSequence := []float64{0, 0, 4, 8, 12, 16}

	for i := 0; i < len(expectedSequence); i++ {
		result, err := curve.Evaluate()
		assert.NoError(t, err, "Loop %d", i)
		assert.InDelta(t, expectedSequence[i], result, 0.001, "Speed should ramp up. Loop %d", i)
		tick()
	}
	// Check sensor wasn't modified
	assert.Equal(t, 62000.0, sensor.MovingAvg)
}

func TestPidCurve_DerivativeAction_RisingTemp(t *testing.T) {
	// Using only D term (P=0, I=0). Negative D gain needed for cooling control.
	curve, sensor, tick := setupTest(t, 0, 0, testD, 60.0) // Start at setpoint.

	// Step 0 (initialization): Temp = 60, Error = 0 -> Output = 0
	result, err := curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 0, result, 0.001, "Step 0")
	tick()
	sensor.MovingAvg += 1000 // Temp becomes 61 (+1C)

	// Step 1: Temp = 61. Measured changed by +1C (from 60) in 0.2s, so the rate is +5C/s.
	// D term = -D * rate = -(-10) * 5 = +50. Output = 50.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 50, result, 0.001, "Step 1 - Output should jump to 50 due to D")
	tick()
	sensor.MovingAvg += 1000 // Temp becomes 62 (+1C again)

	// Step 2: Temp = 62. Measured changed by +1C (from 61), so the rate is still +5C/s.
	// D term = +50. Output = 50.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 50, result, 0.001, "Step 2 - Output should remain 50 due to constant D term")
}

func TestPidCurve_DerivativeAction_FallingTemp(t *testing.T) {
	// Using only D term (P=0, I=0). Negative D gain needed.
	curve, sensor, tick := setupTest(t, 0, 0, testD, 60.0) // Start at setpoint.

	// Step 0 (initialization): Temp = 60, Error = 0 -> Output = 0
	result, err := curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 0, result, 0.001, "Step 0")
	tick()
	sensor.MovingAvg -= 1000 // Temp becomes 59 (-1C)

	// Step 1: Temp = 59. Measured changed by -1C (from 60) in 0.2s, so the rate is -5C/s.
	// D term = -D * rate = -(-10) * -5 = -50. Output = -50, clamped to 0 by pidLoop.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.Equal(t, 0.0, result, "Step 1 - Output should be clamped to 0 due to negative D term")
	tick()
	sensor.MovingAvg -= 1000 // Temp becomes 58 (-1C again)

	// Step 2: Temp = 58. Measured changed by -1C (from 59), so the rate is still -5C/s.
	// D term = -50. Output clamped to 0.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.Equal(t, 0.0, result, "Step 2 - Output should remain 0")
}

func TestPidCurve_Combined_StepUpAndHold(t *testing.T) {
	curve, sensor, tick := setupTest(t, testP, testI, testD, 55.0) // Start below setpoint

	// Step 0 (initialization): Temp = 55, Error = 5, P = -100 -> Output clamped to 0.
	result, err := curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 0, result, 0.001, "Step 0")
	tick()

	// Step 1: Temp jumps to 65. Error = -5. Rate = +50C/s (10C in 0.2s).
	// P = -20 * -5 = 100. The integral is still 0, so I = 0. D = -(-10) * 50 = 500.
	// Output = 100 + 0 + 500 = 600, clamped to 255.
	// Afterwards, the integral becomes -5 * 0.2 = -1.0.
	sensor.MovingAvg = 65000.0
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 255, result, 0.001, "Step 1 - Jump high (saturated) on temp step up due to P and D")
	tick()

	// Step 2: Temp = 65 constant. Error = -5. Rate = 0, so D = 0.
	// P = 100. I term = -10 * -1.0 = 10. Output = 110.
	// Afterwards, the integral becomes -1.0 + -5 * 0.2 = -2.0. The previous output was 255, but the
	// anti-windup guard only triggers for a positive error, so it does not stop the integration.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 110, result, 0.001, "Step 2 - Settle based on P+I, D=0")
	tick()

	// Step 3: P = 100. I term = -10 * -2.0 = 20. D = 0. Output = 120.
	// Afterwards, the integral becomes -3.0.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 120, result, 0.001, "Step 3 - Integral continues increasing output")
	tick()

	// Step 4: P = 100. I term = -10 * -3.0 = 30. D = 0. Output = 130.
	lastResult := result
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 130, result, 0.001, "Step 4 - Output still increasing")
	assert.GreaterOrEqual(t, result, lastResult, "Step 4 - Output should be >= previous")
}
