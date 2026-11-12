package curves

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingCurve records the order of evaluations in log, and depends on the curves in deps.
type recordingCurve struct {
	id    string
	deps  []string
	log   *[]string
	panic bool
	mu    sync.Mutex
	value float64
	last  evaluation
}

func (c *recordingCurve) GetId() string          { return c.id }
func (c *recordingCurve) dependencies() []string { return c.deps }
func (c *recordingCurve) Evaluate() (float64, error) {
	*c.log = append(*c.log, c.id)
	if c.panic {
		panic("curve exploded")
	}
	return c.storeResult(float64(len(*c.log)), nil, time.Unix(1, 0))
}
func (c *recordingCurve) CurrentValue() float64 { return c.CurrentResult().Value }
func (c *recordingCurve) CurrentResult() CurveResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last.result(c.value)
}
func (c *recordingCurve) storeResult(value float64, err error, now time.Time) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.value = value
	}
	c.last = evaluation{err: err, at: now}
	return c.value, err
}

func TestEvaluationOrder_SubCurvesBeforeTheCurvesUsingThem(t *testing.T) {
	var log []string
	curves := []SpeedCurve{
		// "a" sorts first, but depends on all others
		&recordingCurve{id: "a", deps: []string{"m", "z"}, log: &log},
		&recordingCurve{id: "z", deps: []string{"y"}, log: &log},
		&recordingCurve{id: "y", log: &log},
		&recordingCurve{id: "m", deps: []string{"y"}, log: &log},
		&recordingCurve{id: "unused", log: &log},
	}
	evaluator, err := NewCurveEvaluator(curves, time.Second)
	require.NoError(t, err)

	evaluator.RunCycle()

	// every curve exactly once, dependencies first
	assert.Equal(t, []string{"y", "m", "z", "a", "unused"}, log)
}

// Function curves read the results of their sub-curves instead of evaluating them again, so a sub-curve
// that is used by several function curves is still evaluated only once per cycle.
func TestCurveEvaluator_SharedSubCurves_EvaluatedOncePerCycle(t *testing.T) {
	var log []string
	reg := NewMockRegistry()
	a := &recordingCurve{id: "a", log: &log}
	b := &recordingCurve{id: "b", log: &log}
	reg.RegisterCurve(a)
	reg.RegisterCurve(b)
	curves := []SpeedCurve{a, b}
	for _, id := range []string{"f1", "f2"} {
		function, err := NewSpeedCurve(createFunctionCurveConfig(id, configuration.FunctionSum, []string{"a", "b"}))
		require.NoError(t, err)
		reg.RegisterCurve(function)
		curves = append(curves, function)
	}
	evaluator, err := NewCurveEvaluator(curves, time.Second)
	require.NoError(t, err)

	evaluator.RunCycle()
	evaluator.RunCycle()

	assert.Equal(t, []string{"a", "b", "a", "b"}, log)
	// the values of the second cycle: a = 3, b = 4
	f1, _ := reg.GetCurve("f1")
	assert.Equal(t, CurveResult{Value: 7, EvaluatedAt: f1.CurrentResult().EvaluatedAt}, f1.CurrentResult())
}

func TestNewCurveEvaluator_InvalidReferences_ReturnError(t *testing.T) {
	var log []string
	_, err := NewCurveEvaluator([]SpeedCurve{
		&recordingCurve{id: "a", deps: []string{"b"}, log: &log},
		&recordingCurve{id: "b", deps: []string{"c"}, log: &log},
		&recordingCurve{id: "c", deps: []string{"a"}, log: &log},
	}, time.Second)
	assert.ErrorContains(t, err, "curve dependency cycle: a -> b -> c -> a")

	_, err = NewCurveEvaluator([]SpeedCurve{&recordingCurve{id: "a", deps: []string{"a"}, log: &log}}, time.Second)
	assert.ErrorContains(t, err, "curve dependency cycle: a -> a")

	_, err = NewCurveEvaluator([]SpeedCurve{&recordingCurve{id: "a", deps: []string{"missing"}, log: &log}}, time.Second)
	assert.ErrorContains(t, err, "curve 'a' references unknown curve 'missing'")

	_, err = NewCurveEvaluator([]SpeedCurve{&recordingCurve{id: "a", log: &log}, &recordingCurve{id: "a", log: &log}}, time.Second)
	assert.ErrorContains(t, err, "duplicate curve id 'a'")

	_, err = NewCurveEvaluator(nil, 0)
	assert.Error(t, err)
}

// newEvaluatorTestCurves registers a linear and a staircase curve on sensor, a function curve
// (maximum) over both, and a linear curve that no fan uses.
func newEvaluatorTestCurves(t *testing.T, sensor sensors.Sensor) (*MockRegistry, []SpeedCurve) {
	reg := NewMockRegistry()
	reg.RegisterSensor(sensor)
	var result []SpeedCurve
	for _, config := range []configuration.CurveConfig{
		createFunctionCurveConfig("function", configuration.FunctionMaximum, []string{"linear", "staircase"}),
		createLinearCurveConfig("linear", sensor.GetId(), 40, 80),
		createStaircaseCurveConfig("staircase", sensor.GetId(), configuration.HysteresisConfig{}, map[int]float64{40: 50, 60: 200}),
		createLinearCurveConfig("unused", sensor.GetId(), 0, 100),
	} {
		curve, err := NewSpeedCurve(config)
		require.NoError(t, err)
		reg.RegisterCurve(curve)
		result = append(result, curve)
	}
	return reg, result
}

// A function curve sees the values its sub-curves got in the same cycle, and every curve
// (also one that no fan uses) gets a result in every cycle.
func TestCurveEvaluator_Run_EvaluatesAllCurvesEveryCycle(t *testing.T) {
	setUpStaleTimeout(t, time.Hour)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	sensor := &MockSensor{ID: "sensor", MovingAvg: 50000, ReadingTime: clock.Now}
	reg, curves := newEvaluatorTestCurves(t, sensor)
	evaluator, err := NewCurveEvaluator(curves, time.Second)
	require.NoError(t, err)
	evaluator.SetClock(clock)
	cycles := evaluator.Subscribe()

	for _, curve := range curves {
		assert.ErrorIs(t, curve.CurrentResult().Err, ErrCurveNotEvaluated, curve.GetId())
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- evaluator.Run(ctx) }()

	// THEN: the first cycle runs right away
	awaitCycle(t, cycles)
	results := currentResults(reg)
	assert.InDelta(t, 255*0.25, results["linear"].Value, 0.001)
	assert.Equal(t, 50.0, results["staircase"].Value)
	assert.InDelta(t, 255*0.25, results["function"].Value, 0.001)
	assert.InDelta(t, 255*0.5, results["unused"].Value, 0.001)
	for id, result := range results {
		assert.NoError(t, result.Err, id)
		assert.Equal(t, time.Unix(1000, 0), result.EvaluatedAt, id)
	}

	// WHEN: the temperature changes, and the next tick is due
	sensor.MovingAvg = 70000
	clock.BlockUntil(1)
	clock.Advance(time.Second)

	// THEN: all curves (including the function curve) have the values of the new cycle
	awaitCycle(t, cycles)
	results = currentResults(reg)
	assert.InDelta(t, 255*0.75, results["linear"].Value, 0.001)
	assert.Equal(t, 200.0, results["staircase"].Value)
	assert.Equal(t, 200.0, results["function"].Value)
	assert.InDelta(t, 255*0.7, results["unused"].Value, 0.001)
	for id, result := range results {
		assert.Equal(t, time.Unix(1001, 0), result.EvaluatedAt, id)
	}

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("evaluator did not stop")
	}
}

func awaitCycle(t *testing.T, cycles <-chan struct{}) {
	t.Helper()
	select {
	case <-cycles:
	case <-time.After(5 * time.Second):
		t.Fatal("no evaluator cycle")
	}
}

func currentResults(reg *MockRegistry) map[string]CurveResult {
	result := map[string]CurveResult{}
	for id, curve := range reg.curves {
		result[id] = curve.CurrentResult()
	}
	return result
}

// The error of a sub-curve is the error of the function curve's result, which keeps its previous value.
func TestCurveEvaluator_SubCurveError_PropagatesToFunctionCurve(t *testing.T) {
	setUpStaleTimeout(t, time.Hour)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	sensor := &MockSensor{ID: "sensor", MovingAvg: 70000, ReadingTime: clock.Now}
	reg, curves := newEvaluatorTestCurves(t, sensor)
	evaluator, err := NewCurveEvaluator(curves, time.Second)
	require.NoError(t, err)
	evaluator.SetClock(clock)
	evaluator.RunCycle()
	function, _ := reg.GetCurve("function")
	require.NoError(t, function.CurrentResult().Err)

	// WHEN
	sensor.NoReading = true
	clock.Advance(time.Second)
	evaluator.RunCycle()

	// THEN
	result := function.CurrentResult()
	assert.ErrorIs(t, result.Err, sensors.ErrSensorStale)
	assert.Equal(t, 200.0, result.Value, "the previous value")
	assert.Equal(t, time.Unix(1001, 0), result.EvaluatedAt)
}

// A panic while evaluating a curve is stored as the error of its result. The other curves are still
// evaluated, and the subscribers are still notified.
func TestCurveEvaluator_PanicInCurve_StoredAsErrorAndCycleCompletes(t *testing.T) {
	var log []string
	exploding := &recordingCurve{id: "exploding", log: &log, panic: true}
	other := &recordingCurve{id: "other", log: &log}
	evaluator, err := NewCurveEvaluator([]SpeedCurve{exploding, other}, time.Second)
	require.NoError(t, err)
	cycles := evaluator.Subscribe()

	evaluator.RunCycle()

	assert.Equal(t, []string{"exploding", "other"}, log)
	assert.ErrorContains(t, exploding.CurrentResult().Err, "panic while evaluating curve exploding: curve exploded")
	assert.NoError(t, other.CurrentResult().Err)
	awaitCycle(t, cycles)
}

// A subscriber that doesn't keep up doesn't block the evaluator, and finds one pending notification.
func TestCurveEvaluator_SlowSubscriber_DoesNotBlock(t *testing.T) {
	var log []string
	evaluator, err := NewCurveEvaluator([]SpeedCurve{&recordingCurve{id: "a", log: &log}}, time.Second)
	require.NoError(t, err)
	slow := evaluator.Subscribe()
	fast := evaluator.Subscribe()

	for range 3 {
		evaluator.RunCycle()
		awaitCycle(t, fast)
	}

	assert.Len(t, log, 3)
	assert.Len(t, slow, 1)
	awaitCycle(t, slow)
	select {
	case <-slow:
		t.Fatal("more than one pending notification")
	default:
	}
}

// The evaluator evaluates a PID curve once per cycle, no matter how many fans and function curves use it,
// so it steps once per cycle (here: once per new sensor reading, which arrives every cycle).
func TestCurveEvaluator_SharedPidCurve_StepsOncePerCycle(t *testing.T) {
	setUpStaleTimeout(t, time.Hour)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	sensor := &rawReadingSensor{}
	reg := NewMockRegistry()
	reg.RegisterSensor(sensor)
	pid, err := NewSpeedCurve(createPidCurveConfig("pid", sensor.GetId(), 60, testP, testI, testD))
	require.NoError(t, err)
	reg.RegisterCurve(pid)
	// two function curves using the PID curve
	var curves = []SpeedCurve{pid}
	for _, id := range []string{"f1", "f2"} {
		function, err := NewSpeedCurve(createFunctionCurveConfig(id, configuration.FunctionAverage, []string{"pid", "pid"}))
		require.NoError(t, err)
		reg.RegisterCurve(function)
		curves = append(curves, function)
	}
	evaluator, err := NewCurveEvaluator(curves, time.Second)
	require.NoError(t, err)
	evaluator.SetClock(clock)

	// the PID loop as the curve creates it, stepped once per reading
	reference := util.NewPidLoop(testP, testI, testD, 0, 255, true, false)
	temps := []float64{65, 66, 64, 63, 61, 60}
	for i, temp := range temps {
		readingTime := clock.Now()
		sensor.RecordReading(sensors.Reading{Value: temp * 1000, Time: readingTime})
		expected := reference.LoopAt(readingTime, 60, temp)

		evaluator.RunCycle()

		assert.InDelta(t, expected, pid.CurrentResult().Value, 0.0001, "cycle %d", i)
		f1, _ := reg.GetCurve("f1")
		assert.InDelta(t, expected, f1.CurrentResult().Value, 0.0001, "cycle %d", i)
		clock.Advance(time.Second)
	}
}

// Results are written by the evaluator while fan controllers, the REST API and the statistics read them.
func TestCurveEvaluator_ConcurrentCyclesAndReads(t *testing.T) {
	setUpStaleTimeout(t, time.Hour)
	sensor := &rawReadingSensor{}
	sensor.RecordReading(sensors.Reading{Value: 60000, Time: time.Now()})
	sensor.SetMovingAvg(60000)
	reg := NewMockRegistry()
	reg.RegisterSensor(sensor)
	var curves []SpeedCurve
	for _, config := range []configuration.CurveConfig{
		createPidCurveConfig("pid", sensor.GetId(), 60, testP, testI, testD),
		createStaircaseCurveConfig("staircase", sensor.GetId(), configuration.HysteresisConfig{Down: 2}, map[int]float64{40: 50, 60: 200}),
		createFunctionCurveConfig("function", configuration.FunctionMaximum, []string{"pid", "staircase"}),
	} {
		curve, err := NewSpeedCurve(config)
		require.NoError(t, err)
		reg.RegisterCurve(curve)
		curves = append(curves, curve)
	}
	evaluator, err := NewCurveEvaluator(curves, time.Second)
	require.NoError(t, err)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			sensor.RecordReading(sensors.Reading{Value: 60000 + float64(i%10)*1000, Time: time.Now()})
			sensor.SetMovingAvg(60000 + float64(i%10)*1000)
			evaluator.RunCycle()
		}
		close(stop)
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, curve := range curves {
				_ = curve.CurrentResult()
				_ = curve.CurrentValue()
				_, err := curve.(interface{ MarshalJSON() ([]byte, error) }).MarshalJSON()
				assert.NoError(t, err)
			}
		}
	}()
	wg.Wait()
	for _, curve := range curves {
		assert.NoError(t, curve.CurrentResult().Err, curve.GetId())
	}
}
