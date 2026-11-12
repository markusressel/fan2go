package curves

import (
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setUpStaleTimeout sets the sensorStaleTimeout for a test.
func setUpStaleTimeout(t *testing.T, timeout time.Duration) {
	originalConfig := configuration.CurrentConfig
	t.Cleanup(func() { configuration.CurrentConfig = originalConfig })
	configuration.CurrentConfig.SensorStaleTimeout = &timeout
}

// newSensorBoundCurves creates one curve of each sensor based type for the given sensor,
// with the given clock, plus a function curve over the linear and the staircase curve.
func newSensorBoundCurves(t *testing.T, sensor sensors.Sensor, clock util.Clock) []SpeedCurve {
	reg := NewMockRegistry()
	reg.RegisterSensor(sensor)
	configs := []configuration.CurveConfig{
		createLinearCurveConfig("linear", sensor.GetId(), 40, 80),
		createStaircaseCurveConfig("staircase", sensor.GetId(), configuration.HysteresisConfig{}, map[int]float64{40: 50, 60: 100}),
		createPidCurveConfig("pid", sensor.GetId(), 60, -1, 0, 0),
	}
	var result []SpeedCurve
	for _, config := range configs {
		curve, err := NewSpeedCurve(config)
		require.NoError(t, err)
		curve.(interface{ SetClock(util.Clock) }).SetClock(clock)
		reg.RegisterCurve(curve)
		result = append(result, curve)
	}
	function, err := NewSpeedCurve(createFunctionCurveConfig("function", configuration.FunctionMaximum, []string{"linear", "staircase"}))
	require.NoError(t, err)
	function.(interface{ SetClock(util.Clock) }).SetClock(clock)
	reg.RegisterCurve(function)
	return append(result, function)
}

func TestCurves_SensorWithoutReading_ReturnErrSensorStale(t *testing.T) {
	setUpStaleTimeout(t, 10*time.Second)
	sensor := &MockSensor{ID: "sensor", MovingAvg: 70000, NoReading: true}
	clock := util.NewFakeClock(time.Unix(1000, 0))

	for _, curve := range newSensorBoundCurves(t, sensor, clock) {
		value, err := curve.Evaluate()
		assert.ErrorIs(t, err, sensors.ErrSensorStale, curve.GetId())
		assert.Equal(t, 0.0, value, curve.GetId())
	}
}

func TestCurves_StaleSensor_ReturnErrSensorStaleAndPreviousValue(t *testing.T) {
	setUpStaleTimeout(t, 10*time.Second)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	readingTime := clock.Now()
	sensor := &MockSensor{ID: "sensor", MovingAvg: 70000, ReadingTime: func() time.Time { return readingTime }}
	curves := newSensorBoundCurves(t, sensor, clock)

	// GIVEN: each curve was evaluated with a fresh reading
	previous := map[string]float64{}
	for _, curve := range curves {
		value, err := curve.Evaluate()
		require.NoError(t, err, curve.GetId())
		previous[curve.GetId()] = value
	}
	assert.InDelta(t, 255*0.75, previous["linear"], 0.001)
	assert.Equal(t, 100.0, previous["staircase"])
	assert.Equal(t, 10.0, previous["pid"])
	assert.InDelta(t, 255*0.75, previous["function"], 0.001)

	// still fresh at exactly the timeout
	clock.Advance(10 * time.Second)
	for _, curve := range curves {
		_, err := curve.Evaluate()
		assert.NoError(t, err, curve.GetId())
	}

	// WHEN: no new reading arrives for longer than the timeout
	clock.Advance(time.Millisecond)

	// THEN: every curve fails with ErrSensorStale and returns its previous value
	for _, curve := range curves {
		value, err := curve.Evaluate()
		assert.ErrorIs(t, err, sensors.ErrSensorStale, curve.GetId())
		assert.Contains(t, err.Error(), "curve "+curve.GetId(), curve.GetId())
		assert.Equal(t, previous[curve.GetId()], value, curve.GetId())
	}
}

// The PID curve uses the last raw reading recorded by the sensor monitor, not the moving average,
// and doesn't read the sensor itself.
func TestPidCurve_UsesLastRawReading(t *testing.T) {
	setUpStaleTimeout(t, 10*time.Second)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	sensor := &rawReadingSensor{}
	sensor.SetMovingAvg(50000)
	sensor.RecordReading(sensors.Reading{Value: 70000, Time: clock.Now()})
	reg := NewMockRegistry()
	reg.RegisterSensor(sensor)
	curve, err := NewSpeedCurve(createPidCurveConfig("pid", sensor.GetId(), 60, -1, 0, 0))
	require.NoError(t, err)
	curve.(*PidSpeedCurve).SetClock(clock)
	reg.RegisterCurve(curve)

	value, err := curve.Evaluate()

	require.NoError(t, err)
	// P term = -1 * (60 - 70) = 10; the moving average (50 °C) would give 0
	assert.Equal(t, 10.0, value)
}

// rawReadingSensor is a sensor whose GetValue must not be called.
type rawReadingSensor struct {
	sensors.HwmonSensor
}

func (s *rawReadingSensor) GetId() string { return "raw" }

func (s *rawReadingSensor) GetValue() (float64, error) {
	panic("curves must not read the sensor")
}

// The PID loop only steps when a new reading arrived. Evaluating the curve more often than the sensor is
// read doesn't add to the I term, and the time between two readings is used as dt.
func TestPidCurve_StepsOnlyOnNewReadings(t *testing.T) {
	setUpStaleTimeout(t, 10*time.Second)
	// I only: the I term lags one step behind (see pid_test.go)
	curve, sensor, _ := setupTest(t, 0, testI, 0, 61.0)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	readingTime := clock.Now()
	sensor.ReadingTime = func() time.Time { return readingTime }
	curve.SetClock(clock)

	// Step 0 (initialization): Output = P term = 0.
	result, err := curve.Evaluate()
	require.NoError(t, err)
	assert.Equal(t, 0.0, result)

	// a new reading 1s later: Step 1, I = 0 (lags), afterwards integral = -1 * 1s = -1
	readingTime = readingTime.Add(time.Second)
	clock.Advance(time.Second)
	result, err = curve.Evaluate()
	require.NoError(t, err)
	assert.Equal(t, 0.0, result)

	// WHEN: the curve is evaluated several times while no new reading arrives
	// (PidLoop.LoopAt would also return its last output for dt = 0; the curve doesn't even call it then)
	for i := 0; i < 5; i++ {
		clock.Advance(200 * time.Millisecond)
		result, err = curve.Evaluate()
		require.NoError(t, err)
		assert.Equal(t, 0.0, result, "no new reading, the previous value is returned")
	}

	// THEN: the next reading (2s after the previous one) steps the loop once,
	// with the integral of exactly one step: I = -10 * -1 = 10
	readingTime = readingTime.Add(2 * time.Second)
	result, err = curve.Evaluate()
	require.NoError(t, err)
	assert.InDelta(t, 10.0, result, 0.001)

	// and the step after that uses dt = 2s for the previous reading: integral = -1 + -1 * 2 = -3, I = 30
	readingTime = readingTime.Add(time.Second)
	clock.Advance(3 * time.Second)
	result, err = curve.Evaluate()
	require.NoError(t, err)
	assert.InDelta(t, 30.0, result, 0.001)
}

// After the sensor value was stale, the PID loop starts over, instead of applying the whole outage
// as one step to the I term.
func TestPidCurve_StartsOverAfterStaleSensor(t *testing.T) {
	setUpStaleTimeout(t, 10*time.Second)
	// I only: the I term lags one step behind (see pid_test.go)
	curve, sensor, _ := setupTest(t, 0, testI, 0, 61.0)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	readingTime := clock.Now()
	sensor.ReadingTime = func() time.Time { return readingTime }
	curve.SetClock(clock)

	// two steps 1s apart: afterwards integral = -1 * 1s = -1
	_, err := curve.Evaluate()
	require.NoError(t, err)
	readingTime = readingTime.Add(time.Second)
	clock.Advance(time.Second)
	_, err = curve.Evaluate()
	require.NoError(t, err)

	// WHEN: the sensor value is stale for a minute, and then a new reading arrives
	clock.Advance(time.Minute)
	_, err = curve.Evaluate()
	require.ErrorIs(t, err, sensors.ErrSensorStale)
	readingTime = clock.Now()
	result, err := curve.Evaluate()

	// THEN: the loop starts over (output = P term = 0), instead of I = -10 * (-1 - 60) = 610 (clamped to 255)
	require.NoError(t, err)
	assert.Equal(t, 0.0, result)

	// and the next step only uses the integral of the time after the outage: I = -10 * (-1 * 1s) = 10
	readingTime = readingTime.Add(time.Second)
	clock.Advance(time.Second)
	result, err = curve.Evaluate()
	require.NoError(t, err)
	assert.InDelta(t, 0.0, result, 0.001, "the I term lags one step")
	readingTime = readingTime.Add(time.Second)
	clock.Advance(time.Second)
	result, err = curve.Evaluate()
	require.NoError(t, err)
	assert.InDelta(t, 10.0, result, 0.001)
}

func TestFunctionCurve_SubCurveError_KeepsPreviousValue(t *testing.T) {
	setUpStaleTimeout(t, 10*time.Second)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	sensor := &MockSensor{ID: "sensor", MovingAvg: 70000, ReadingTime: clock.Now}
	curves := newSensorBoundCurves(t, sensor, clock)
	function := curves[len(curves)-1]
	// evaluates the sub-curves before the function curve, like the curve evaluator
	evaluateAll := func() (float64, error) {
		for _, curve := range curves[:len(curves)-1] {
			_, _ = curve.Evaluate()
		}
		return function.Evaluate()
	}
	value, err := evaluateAll()
	require.NoError(t, err)

	sensor.NoReading = true
	failedValue, err := evaluateAll()

	assert.ErrorIs(t, err, sensors.ErrSensorStale)
	assert.Equal(t, value, failedValue)
	assert.Equal(t, value, function.CurrentValue())
	result := function.CurrentResult()
	assert.ErrorIs(t, result.Err, sensors.ErrSensorStale, "the result carries the error of the sub-curve")
	assert.Equal(t, value, result.Value)
	assert.Equal(t, clock.Now(), result.EvaluatedAt)
}

// The sensor monitor records readings while fan controllers evaluate a shared PID curve.
func TestPidCurve_ConcurrentEvaluateAndReadings(t *testing.T) {
	setUpStaleTimeout(t, time.Hour)
	sensor := &rawReadingSensor{}
	start := time.Now()
	sensor.RecordReading(sensors.Reading{Value: 60000, Time: start})
	reg := NewMockRegistry()
	reg.RegisterSensor(sensor)
	curve, err := NewSpeedCurve(createPidCurveConfig("pid", sensor.GetId(), 60, testP, testI, testD))
	require.NoError(t, err)
	reg.RegisterCurve(curve)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 1; i <= 500; i++ {
			sensor.RecordReading(sensors.Reading{Value: 60000 + float64(i), Time: start.Add(time.Duration(i) * time.Millisecond)})
		}
	}()
	for range 2 {
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_, err := curve.Evaluate()
				assert.NoError(t, err)
				_ = curve.CurrentValue()
			}
		}()
	}
	wg.Wait()
}
