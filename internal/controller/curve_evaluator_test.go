package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file run fan controllers driven by the curve evaluator, the same way the daemon does.
// Run them with -race.

const evaluatorTestTickRate = 100 * time.Millisecond

// evaluatorTest is a registry with a sensor and stateful curves that are shared between fans:
//   - staircase: a staircase curve (40°C: 100, 60°C: 200, hysteresis down 2°C)
//   - pid: a PID curve with setpoint 60°C
//   - function: the maximum of staircase and pid
type evaluatorTest struct {
	t         *testing.T
	clock     *util.FakeClock
	sensor    *sensors.HwmonSensor
	reg       *registry.Registry
	curves    []curves.SpeedCurve
	evaluator *curves.CurveEvaluator
}

func newEvaluatorTest(t *testing.T) *evaluatorTest {
	t.Cleanup(setUpRunTestConfig(time.Hour))
	staleTimeout := time.Hour
	configuration.CurrentConfig.SensorStaleTimeout = &staleTimeout

	test := &evaluatorTest{t: t, clock: util.NewFakeClock(time.Unix(1000, 0))}
	test.sensor = &sensors.HwmonSensor{Config: configuration.SensorConfig{ID: "sensor"}}
	test.recordTemp(50)
	test.reg = registry.NewRegistry()
	test.reg.RegisterSensor(test.sensor)
	for _, config := range []configuration.CurveConfig{
		{ID: "staircase", Staircase: &configuration.StaircaseCurveConfig{
			Sensor: "sensor", Hysteresis: configuration.HysteresisConfig{Down: 2}, Steps: map[int]float64{40: 100, 60: 200},
		}},
		{ID: "pid", PID: &configuration.PidCurveConfig{Sensor: "sensor", SetPoint: 60, P: -5, I: -1, D: -1}},
		{ID: "function", Function: &configuration.FunctionCurveConfig{
			Type: configuration.FunctionMaximum, Curves: []string{"staircase", "pid"},
		}},
	} {
		curve, err := curves.NewSpeedCurve(config)
		require.NoError(t, err)
		test.reg.RegisterCurve(curve)
		test.curves = append(test.curves, curve)
	}
	evaluator, err := curves.NewCurveEvaluator(test.curves, evaluatorTestTickRate)
	require.NoError(t, err)
	evaluator.SetClock(test.clock)
	test.evaluator = evaluator
	return test
}

// recordTemp records a new reading of the sensor at the current time, like the sensor monitor.
func (test *evaluatorTest) recordTemp(celsius float64) {
	test.sensor.SetMovingAvg(celsius * 1000)
	test.sensor.RecordReading(sensors.Reading{Value: celsius * 1000, Time: test.clock.Now()})
}

func (test *evaluatorTest) curve(id string) curves.SpeedCurve {
	curve, ok := test.reg.GetCurve(id)
	require.True(test.t, ok, id)
	return curve
}

// newController returns a controller for a new fan using the given curve, driven by the evaluator.
func (test *evaluatorTest) newController(fanId string, curveId string) (*DefaultFanController, *panickingFan) {
	fan := newPanickingFan()
	fan.ID = fanId
	controller := newRunTestController(fan, test.curve(curveId), evaluatorTestTickRate, test.clock)
	controller.SetCurveCycles(test.evaluator.Subscribe())
	return controller, fan
}

// startEvaluator runs the evaluator until the returned function is called, which waits until it has stopped.
func (test *evaluatorTest) startEvaluator() (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- test.evaluator.Run(ctx) }()
	return func() {
		cancel()
		assert.NoError(test.t, awaitRun(test.t, done))
	}
}

// awaitPwm advances the time by one evaluator tick at a time, until all fans have the given PWM values.
func (test *evaluatorTest) awaitPwm(expected map[*panickingFan]int) {
	test.t.Helper()
	require.Eventually(test.t, func() bool {
		test.clock.Advance(evaluatorTestTickRate)
		for fan, pwm := range expected {
			if current, _ := fan.GetPwm(); current != pwm {
				return false
			}
		}
		return true
	}, 10*time.Second, time.Millisecond)
}

// Two fans share a staircase curve, and a function curve uses it and a PID curve, which a fourth fan uses too.
// The curves are evaluated by the evaluator only (once per cycle), while the controllers, a sensor monitor
// and the REST API run concurrently. Without the evaluator, each controller evaluated its curve (and the
// sub-curves) itself, concurrently with the others, which races on the state of the shared curves.
func TestCurveEvaluator_SharedStatefulCurves_ConcurrentControllersMonitorAndApi(t *testing.T) {
	test := newEvaluatorTest(t)
	ctrlA, fanA := test.newController("a", "staircase")
	ctrlB, fanB := test.newController("b", "staircase")
	ctrlC, fanC := test.newController("c", "function")
	ctrlD, fanD := test.newController("d", "pid")

	stopEvaluator := test.startEvaluator()
	ctx, cancel := context.WithCancel(context.Background())
	var runs []<-chan error
	for _, controller := range []*DefaultFanController{ctrlA, ctrlB, ctrlC, ctrlD} {
		runs = append(runs, startRun(controller, ctx))
	}

	// a sensor monitor and the REST API run concurrently
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			test.recordTemp(float64(55 + i%10))
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, curve := range test.curves {
				_, err := json.Marshal(curve)
				assert.NoError(t, err)
				_ = curve.CurrentValue()
			}
		}
	}()

	// WHEN: the controllers start and run until each of them changed the speed of its fan a few times
	pwmWrites := func(fan *panickingFan) int {
		count := 0
		for _, write := range fan.getWrites() {
			if write == "pwm" {
				count++
			}
		}
		return count
	}
	require.Eventually(t, func() bool {
		test.clock.Advance(evaluatorTestTickRate)
		for _, fan := range []*panickingFan{fanA, fanB, fanC, fanD} {
			if pwmWrites(fan) < 5 {
				return false
			}
		}
		return true
	}, 10*time.Second, time.Millisecond)
	close(stop)
	wg.Wait()

	// THEN: once the temperature is stable, all fans follow their curves
	test.recordTemp(70)
	pidValue := func() int {
		return int(test.curve("pid").CurrentValue() + 0.5)
	}
	require.Eventually(t, func() bool {
		test.clock.Advance(evaluatorTestTickRate)
		a, _ := fanA.GetPwm()
		b, _ := fanB.GetPwm()
		c, _ := fanC.GetPwm()
		d, _ := fanD.GetPwm()
		return a == 200 && b == 200 && c == max(200, pidValue()) && d == pidValue()
	}, 10*time.Second, time.Millisecond)

	cancel()
	for _, done := range runs {
		assert.NoError(t, awaitRun(t, done))
	}
	stopEvaluator()
}

// A PID curve used by two fans (directly, and through a function curve) is stepped once per evaluator
// cycle, not once per fan: its value is the value of a single PID loop that was stepped once per reading.
func TestCurveEvaluator_PidCurveUsedByTwoFans_SteppedOncePerCycle(t *testing.T) {
	test := newEvaluatorTest(t)
	controllerA, fanA := test.newController("a", "pid")
	controllerB, fanB := test.newController("b", "pid")
	for _, controller := range []*DefaultFanController{controllerA, controllerB} {
		controller.pwmMapping = createOneToOnePwmMap()
		controller.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
	}

	reference := util.NewPidLoop(-5, -1, -1, 0, 255, true, false)
	for i, temp := range []float64{62, 64, 63, 61, 65, 66, 64, 60} {
		// WHEN: a new reading arrives, the evaluator runs a cycle, and both fans update their speed
		test.clock.Advance(evaluatorTestTickRate)
		test.recordTemp(temp)
		expected := reference.LoopAt(test.clock.Now(), 60, temp)
		test.evaluator.RunCycle()
		require.NoError(t, controllerA.UpdateFanSpeed())
		require.NoError(t, controllerB.UpdateFanSpeed())

		// THEN
		assert.InDelta(t, expected, test.curve("pid").CurrentValue(), 0.0001, "cycle %d", i)
		pwmA, _ := fanA.GetPwm()
		pwmB, _ := fanB.GetPwm()
		assert.Equal(t, int(expected+0.5), pwmA, "cycle %d", i)
		assert.Equal(t, pwmA, pwmB, "cycle %d", i)
	}
}

// countingCurve counts the evaluations of the curve it wraps.
type countingCurve struct {
	curves.SpeedCurve
	mu          sync.Mutex
	evaluations int
}

func (c *countingCurve) Evaluate() (float64, error) {
	c.mu.Lock()
	c.evaluations++
	c.mu.Unlock()
	return c.SpeedCurve.Evaluate()
}

func (c *countingCurve) getEvaluations() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.evaluations
}

// A PID curve used by two fans directly and by two function curves (one of them used by a third fan)
// is evaluated exactly once per cycle, even though a new sensor reading arrives every cycle.
func TestCurveEvaluator_SharedPidCurve_EvaluatedOncePerCycle(t *testing.T) {
	t.Cleanup(setUpRunTestConfig(time.Hour))
	staleTimeout := time.Hour
	configuration.CurrentConfig.SensorStaleTimeout = &staleTimeout
	clock := util.NewFakeClock(time.Unix(1000, 0))
	sensor := &sensors.HwmonSensor{Config: configuration.SensorConfig{ID: "sensor"}}
	reg := registry.NewRegistry()
	reg.RegisterSensor(sensor)
	pid, err := curves.NewSpeedCurve(configuration.CurveConfig{ID: "pid", PID: &configuration.PidCurveConfig{Sensor: "sensor", SetPoint: 60, P: -5, I: -1, D: -1}})
	require.NoError(t, err)
	pid.(interface{ BindRegistry(curves.RegistryReader) }).BindRegistry(reg)
	pid.(interface{ SetClock(util.Clock) }).SetClock(clock)
	counting := &countingCurve{SpeedCurve: pid}
	reg.RegisterCurve(counting)
	all := []curves.SpeedCurve{counting}
	for _, id := range []string{"f1", "f2"} {
		function, err := curves.NewSpeedCurve(configuration.CurveConfig{ID: id, Function: &configuration.FunctionCurveConfig{
			Type: configuration.FunctionAverage, Curves: []string{"pid", "pid"},
		}})
		require.NoError(t, err)
		reg.RegisterCurve(function)
		all = append(all, function)
	}
	evaluator, err := curves.NewCurveEvaluator(all, evaluatorTestTickRate)
	require.NoError(t, err)
	evaluator.SetClock(clock)
	var controllers []*DefaultFanController
	for _, curve := range []curves.SpeedCurve{counting, counting, all[1]} {
		fan := newPanickingFan()
		controller := newRunTestController(fan, curve, evaluatorTestTickRate, clock)
		controller.pwmMapping = createOneToOnePwmMap()
		controller.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
		controllers = append(controllers, controller)
	}

	for i := 1; i <= 5; i++ {
		// WHEN: a new reading arrives, the evaluator runs a cycle, and all fans update their speed
		clock.Advance(evaluatorTestTickRate)
		sensor.SetMovingAvg(float64(60+i) * 1000)
		sensor.RecordReading(sensors.Reading{Value: float64(60+i) * 1000, Time: clock.Now()})
		evaluator.RunCycle()
		for _, controller := range controllers {
			require.NoError(t, controller.UpdateFanSpeed())
		}

		// THEN
		assert.Equal(t, i, counting.getEvaluations())
	}
}

// firstReadSignallingCurve closes firstRead when its result is read for the first time.
type firstReadSignallingCurve struct {
	curves.SpeedCurve
	firstRead chan struct{}
	once      sync.Once
}

func (c *firstReadSignallingCurve) CurrentResult() curves.CurveResult {
	result := c.SpeedCurve.CurrentResult()
	c.once.Do(func() { close(c.firstRead) })
	return result
}

// Without an evaluated curve result (the evaluator hasn't run its first cycle yet), the controller waits
// for the first cycle instead of applying its onSensorFailure action.
func TestFanController_Run_CurveNotEvaluatedYet_WaitsForFirstCycle(t *testing.T) {
	test := newEvaluatorTest(t)
	test.recordTemp(65)
	controller, fan := test.newController("a", "staircase")
	curve := &firstReadSignallingCurve{SpeedCurve: test.curve("staircase"), firstRead: make(chan struct{})}
	controller.UpdateCurve(curve)
	var notified []string
	var notifiedMu sync.Mutex
	controller.errorNotifier = func(title, format string, a ...any) {
		notifiedMu.Lock()
		defer notifiedMu.Unlock()
		notified = append(notified, fmt.Sprintf(format, a...))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := startRun(controller, ctx)
	advanceThroughRunStartup(t, test.clock)

	// WHEN: the evaluator runs its first cycle after the update loop has read the (not yet evaluated) curve result
	<-curve.firstRead
	test.clock.Advance(time.Minute)
	test.evaluator.RunCycle()

	// THEN: the first PWM value written is the curve value (not maxPwm because of a curve failure)
	require.Eventually(t, func() bool { pwm, _ := fan.GetPwm(); return pwm == 200 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, []string{"mode", "pwm"}, fan.getWrites())
	cancel()
	assert.NoError(t, awaitRun(t, done))
	notifiedMu.Lock()
	defer notifiedMu.Unlock()
	assert.Empty(t, notified)
}

// The error of a sub-curve reaches the controller of a fan using the function curve,
// which applies its onSensorFailure action, and recovers with the sub-curve.
func TestCurveEvaluator_SubCurveError_ReachesControllerFailureHandling(t *testing.T) {
	test := newEvaluatorTest(t)
	staleTimeout := 10 * time.Second
	configuration.CurrentConfig.SensorStaleTimeout = &staleTimeout
	controller, fan := test.newController("a", "function")
	fan.MaxPWM = 250
	controller.pwmMapping = createOneToOnePwmMap()
	controller.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
	var notified []string
	controller.errorNotifier = func(title, format string, a ...any) {
		notified = append(notified, fmt.Sprintf(format, a...))
	}
	test.recordTemp(65)
	test.evaluator.RunCycle()
	require.NoError(t, controller.UpdateFanSpeed())

	// WHEN: the sensor of the sub-curves becomes stale
	test.clock.Advance(staleTimeout + time.Second)
	test.evaluator.RunCycle()
	err := controller.UpdateFanSpeed()

	// THEN: the controller applies onSensorFailure (default: max)
	assert.ErrorIs(t, err, ErrCurveEvaluation)
	assert.ErrorIs(t, err, sensors.ErrSensorStale)
	pwm, _ := fan.GetPwm()
	assert.Equal(t, 250, pwm)
	require.Len(t, notified, 1)
	assert.Contains(t, notified[0], "curve function: curve staircase:")

	// WHEN: the sensor recovers
	test.recordTemp(65)
	test.evaluator.RunCycle()

	// THEN
	require.NoError(t, controller.UpdateFanSpeed())
	pwm, _ = fan.GetPwm()
	assert.Equal(t, int(test.curve("function").CurrentValue()+0.5), pwm)
}

// The update loop updates the fan speed once per evaluator cycle, and not on its own.
func TestFanController_Run_UpdatesOncePerEvaluatorCycle(t *testing.T) {
	t.Cleanup(setUpRunTestConfig(time.Hour))
	fan := newPanickingFan()
	curve := &switchableCurve{value: 50}
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller := newRunTestController(fan, curve, time.Millisecond, clock)
	cycles := make(chan struct{}, 1)
	controller.SetCurveCycles(cycles)
	ctx, cancel := context.WithCancel(context.Background())
	done := startRun(controller, ctx)
	// warm-up, then RPM monitor ticker + startup delay (no update loop ticker)
	clock.BlockUntil(1)
	clock.Advance(defaultStartupSensorWarmup + configuration.CurrentConfig.TempSensorPollingRate)
	clock.BlockUntil(2)
	clock.Advance(defaultStartupUpdateDelay)

	// the first update happens right after the startup delay
	require.Eventually(t, func() bool { pwm, _ := fan.GetPwm(); return pwm == 50 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, 1, clock.Waiters(), "only the RPM monitor ticker, the update loop has none")
	reads := curve.getEvaluations()

	// WHEN: time passes without evaluator cycles, and then the evaluator signals cycles
	clock.Advance(time.Minute)
	for i := 1; i <= 3; i++ {
		curve.set(float64(50+i), nil)
		cycles <- struct{}{}
		// THEN: one update per cycle (one read of the curve result), with the value of the cycle
		require.Eventually(t, func() bool {
			pwm, _ := fan.GetPwm()
			return pwm == 50+i
		}, 5*time.Second, time.Millisecond)
		assert.Equal(t, reads+i, curve.getEvaluations())
	}
	assert.Equal(t, []string{"mode", "pwm", "pwm", "pwm", "pwm"}, fan.getWrites())

	cancel()
	assert.NoError(t, awaitRun(t, done))
}

// The controller doesn't depend on the evaluator to stop: if the evaluator has stopped already
// (no more cycles), the controller still stops and restores the fan when its context is cancelled.
func TestFanController_Run_EvaluatorStopped_ControllerStopsAndRestores(t *testing.T) {
	test := newEvaluatorTest(t)
	controller, fan := test.newController("a", "staircase")
	test.recordTemp(65)
	stopEvaluator := test.startEvaluator()
	ctx, cancel := context.WithCancel(context.Background())
	done := startRun(controller, ctx)
	test.awaitPwm(map[*panickingFan]int{fan: 200})

	// WHEN: the evaluator stops, and then the controller is cancelled
	stopEvaluator()
	cancel()

	// THEN
	assert.NoError(t, awaitRun(t, done))
	assert.Equal(t, []fans.ControlMode{fans.ControlModePWM, fans.ControlModeAutomatic}, fan.getControlModeWrites())
}
