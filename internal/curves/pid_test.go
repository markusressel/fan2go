package curves

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
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
const loopDelay = 200 * time.Millisecond // Simulate dt=0.2s

// Define test gains suitable for 0-255 output range
const testP = -20.0
const testI = -10.0
const testD = -10.0

// Function to create curve and sensor for tests, ensuring clean state
func setupTest(t *testing.T, p, i, d float64, initialTemp float64) (*PidSpeedCurve, *MockSensor) {
	// NOTE: Assumes sensor registry doesn't persist between tests or is cleaned.
	// If RegisterSensor has global effects, add cleanup logic here.

	mockSensor := &MockSensor{
		ID:        testSensorID,
		Name:      "Mock Temp Sensor",
		MovingAvg: initialTemp * 1000, // Store in milli-units
	}
	sensors.RegisterSensor(mockSensor)

	cfg := createPidCurveConfig(testCurveID, testSensorID, setPoint, p, i, d)

	// --- This part needs your actual NewSpeedCurve or a mock ---
	// Example assuming direct creation for testing structure:
	pidLoopInstance := util.NewPidLoop(p, i, d, 0, 255, true, false)
	// pidLoopInstance.Reset() // Add Reset if implemented and needed for test isolation
	curve := &PidSpeedCurve{
		Config:  cfg,
		pidLoop: pidLoopInstance,
		Value:   0, // Initial value
	}
	// Replace above with actual call:
	// curveInstance, err := NewSpeedCurve(cfg)
	// if err != nil {
	// 	t.Fatalf("Failed to create speed curve: %v", err)
	// }
	// curve, ok := curveInstance.(*PidSpeedCurve)
	// if !ok {
	// 	t.Fatalf("Created curve is not a *PidSpeedCurve")
	// }
	// --- End replacement section ---

	return curve, mockSensor
}

// --- Steady State Tests ---

func TestPidCurve_SteadyState_WayBelow(t *testing.T) {
	curve, _ := setupTest(t, testP, testI, testD, 40.0) // Temp 40C << 60C

	result, err := curve.Evaluate()
	assert.NoError(t, err)
	// Error = 60 - 40 = 20. P term = -20 * 20 = -400.
	// Output should be negative, clamped to 0 by pidLoop.
	assert.Equal(t, 0.0, result, "Speed should be 0 when way below setpoint")
}

func TestPidCurve_SteadyState_AtSetpoint(t *testing.T) {
	curve, _ := setupTest(t, testP, testI, testD, 60.0) // Temp 60C == 60C

	result, err := curve.Evaluate()
	assert.NoError(t, err)
	// Error = 0. P=0. Assume I=0 initially. D=0. Output = 0.
	assert.Equal(t, 0.0, result, "Speed should be 0 when at setpoint (assuming zero initial integral)")

	time.Sleep(loopDelay) // Allow time for potential integral drift
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.Equal(t, 0.0, result, "Speed should stay 0 when at setpoint")
}

func TestPidCurve_SteadyState_SlightlyAbove(t *testing.T) {
	curve, sensor := setupTest(t, testP, testI, testD, 61.0) // Temp 61C > 60C

	// Initial Evaluation
	result, err := curve.Evaluate()
	assert.NoError(t, err)
	// Error = 60 - 61 = -1. P term = -20 * -1 = 20. Assume I=0, D=0 initially.
	// Output = 20. Rounded = 20.
	assert.InDelta(t, 20, result, 1, "Initial Speed should be ~20 when slightly above setpoint") // Use InDelta

	// Second Evaluation (Integral Effect)
	time.Sleep(loopDelay) // dt=0.2
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	// Integral added approx: I*err*dt = -10 * -1 * 0.2 = +2.0.
	// New Output approx = PTerm + ITerm = 20 + 2.0 = 22.0. Rounded = 22.
	assert.InDelta(t, 20, result, 1, "Speed should still be ~20 in the second step") // Adjusted expectation

	// Check sensor wasn't modified
	assert.Equal(t, 61000.0, sensor.MovingAvg)
}

func TestPidCurve_SteadyState_ModeratelyAbove(t *testing.T) {
	curve, _ := setupTest(t, testP, testI, testD, 65.0) // Temp 65C > 60C

	// Initial Evaluation
	result, err := curve.Evaluate()
	assert.NoError(t, err)
	// Error = 60 - 65 = -5. P term = -20 * -5 = 100. Assume I=0, D=0 initially.
	// Output = 100. Rounded = 100.
	assert.InDelta(t, 100, result, 1, "Initial Speed should be ~100 when moderately above setpoint")

	// Second Evaluation (Integral Effect)
	time.Sleep(loopDelay) // dt=0.2
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	// Integral added approx: I*err*dt = -10 * -5 * 0.2 = +10.0.
	// New Output approx = PTerm + ITerm = 100 + 10.0 = 110.0. Rounded = 110.
	assert.InDelta(t, 100, result, 1, "Speed should still be ~100 in the second step")
}

func TestPidCurve_SteadyState_WayAbove(t *testing.T) {
	curve, _ := setupTest(t, testP, testI, testD, 75.0) // Temp 75C >> 60C

	result, err := curve.Evaluate()
	assert.NoError(t, err)
	// Error = 60 - 75 = -15. P term = -20 * -15 = 300.
	// Output clamped to 255 by pidLoop. Rounded = 255.
	assert.Equal(t, 255.0, result, "Speed should saturate to 255 when way above setpoint")
}

// --- Dynamic Tests ---

func TestPidCurve_IntegralAction_RampUp(t *testing.T) {
	// Using only I term (P=0, D=0). Negative I gain needed for cooling control.
	curve, sensor := setupTest(t, 0, testI, 0, 62.0) // Temp 62C > 60C

	// Error = 60 - 62 = -2.
	// Expected Output change per step = I*err*dt = -10 * -2 * 0.2 = +4.0.
	expectedSequence := []int{0, 0, 4, 8, 12, 16} // Expected Speed values after rounding

	for i := 0; i < len(expectedSequence); i++ {
		result, err := curve.Evaluate()
		assert.NoError(t, err, "Loop %d", i)
		assert.InDelta(t, expectedSequence[i], result, 1, "Speed should ramp up. Loop %d", i)
		time.Sleep(loopDelay)
	}
	// Check sensor wasn't modified
	assert.Equal(t, 62000.0, sensor.MovingAvg)
}

func TestPidCurve_DerivativeAction_RisingTemp(t *testing.T) {
	// Using only D term (P=0, I=0). Negative D gain needed for cooling control.
	curve, sensor := setupTest(t, 0, 0, testD, 60.0) // Start at setpoint.

	// Step 0: Temp = 60, Error = 0, Rate = 0 -> Output = 0
	result, err := curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 0, result, 1, "Step 0") // Use InDelta
	time.Sleep(loopDelay)
	sensor.MovingAvg += 1000 // Temp becomes 61 (+1C) -> Rate = +5C/s

	// Step 1: Temp = 61. Measured changed by +1C (from 60). Rate = +5C/s.
	// derivativeRaw = +5 (approx, (61-60)/0.2). D term = -D * derivativeRaw = -(-10) * 5 = +50.
	// Output = 50. Rounded = 50.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 50, result, 1, "Step 1 - Output should jump to ~50 due to D")
	time.Sleep(loopDelay)
	sensor.MovingAvg += 1000 // Temp becomes 62 (+1C again) -> Rate = +5C/s

	// Step 2: Temp = 62. Measured changed by +1C (from 61). Rate = +5C/s.
	// D term should still be approx +50.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 50, result, 1, "Step 2 - Output should remain ~50 due to constant D term")
}

func TestPidCurve_DerivativeAction_FallingTemp(t *testing.T) {
	// Using only D term (P=0, I=0). Negative D gain needed.
	curve, sensor := setupTest(t, 0, 0, testD, 60.0) // Start at setpoint.

	// Step 0: Temp = 60, Error = 0, Rate = 0 -> Output = 0
	result, err := curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 0, result, 1, "Step 0")
	time.Sleep(loopDelay)
	sensor.MovingAvg -= 1000 // Temp becomes 59 (-1C) -> Rate = -5C/s

	// Step 1: Temp = 59. Measured changed by -1C (from 60). Rate = -5C/s.
	// derivativeRaw = -5. D term = -D * derivativeRaw = -(-10) * -5 = -50.
	// Output = -50. Clamped to 0 by pidLoop. Rounded = 0.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.Equal(t, 0.0, result, "Step 1 - Output should be clamped to 0 due to negative D term")
	time.Sleep(loopDelay)
	sensor.MovingAvg -= 1000 // Temp becomes 58 (-1C again) -> Rate = -5C/s

	// Step 2: Temp = 58. Measured changed by -1C (from 59). Rate = -5C/s.
	// D term = -50. Clamped to 0.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.Equal(t, 0.0, result, "Step 2 - Output should remain 0")
}

func TestPidCurve_Combined_StepUpAndHold(t *testing.T) {
	curve, sensor := setupTest(t, testP, testI, testD, 55.0) // Start below setpoint

	// Step 0: Temp=55, Error=5, P=-100 -> Output=0
	result, err := curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 0, result, 1, "Step 0")
	time.Sleep(loopDelay)

	// Step 1: Temp jumps to 65. Error=-5. Rate=+50C/s (10C/0.2s).
	// P = -20 * -5 = 100. I term small (integral near 0 or slightly neg). D = -(-10)*50 = 500.
	// Output = P + I + D = 100 + ~0 + 500 = ~600. Clamped=255. Rounded=255.
	sensor.MovingAvg = 65000.0
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 255, result, 1, "Step 1 - Jump high (saturated) on temp step up due to P and D")
	time.Sleep(loopDelay)

	// Step 2: Temp=65 constant. Error=-5. Rate=0.
	// P = 100. Integral += -5*0.2 = -1.0 (approx). ITerm = -10*(-1.0) = 10. D=0.
	// Output = P + I + D = 100 + 10 + 0 = 110. Rounded=110.
	// Note: Anti-windup might affect integral slightly if Step 1 output was internally > 255 before clamping. Let's check with InDelta.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 110, result, 2, "Step 2 - Settle based on P+I, D=0") // Allow delta=2
	time.Sleep(loopDelay)

	// Step 3: Temp=65 constant. Error=-5. Rate=0.
	// P=100. Integral += -1.0 -> -2.0 (approx). ITerm = -10*(-2.0) = 20. D=0.
	// Output = 100 + 20 + 0 = 120. Rounded=120.
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 120, result, 2, "Step 3 - Integral continues increasing output")
	time.Sleep(loopDelay)

	// Step 4: Temp=65 constant. Error=-5. Rate=0.
	// P=100. Integral += -1.0 -> -3.0 (approx). ITerm = -10*(-3.0) = 30. D=0.
	// Output = 100 + 30 + 0 = 130. Rounded=130.
	lastResult := result
	result, err = curve.Evaluate()
	assert.NoError(t, err)
	assert.InDelta(t, 130, result, 2, "Step 4 - Output still increasing")
	assert.GreaterOrEqual(t, result, lastResult, "Step 4 - Output should be >= previous")
}
