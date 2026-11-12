package internal

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCurveEvaluator_DependencyCycle_ReturnsError(t *testing.T) {
	reg := registry.NewRegistry()
	for _, config := range []configuration.CurveConfig{
		{ID: "a", Function: &configuration.FunctionCurveConfig{Type: configuration.FunctionMaximum, Curves: []string{"b", "b"}}},
		{ID: "b", Function: &configuration.FunctionCurveConfig{Type: configuration.FunctionMaximum, Curves: []string{"a", "a"}}},
	} {
		curve, err := curves.NewSpeedCurve(config)
		require.NoError(t, err)
		reg.RegisterCurve(curve)
	}

	_, err := newCurveEvaluator(reg, time.Second)

	assert.ErrorContains(t, err, "curve dependency cycle")
}

// cyclesAfterStop is the number of evaluator cycles cycleCheckingController waits for after its context is done.
// An evaluator that is stopped together with the controllers may still run a cycle or two (its select
// picks randomly between its ticker and its context), but not this many.

// evaluatorTestTickRate is the tick rate of the evaluator in these tests (driven by a fake clock)
const evaluatorTestTickRate = time.Second
const cyclesAfterStop = 20

// cycleCheckingController is a FanController whose Run waits until its context is done, and then
// for cyclesAfterStop more cycles of the curve evaluator, to check that the evaluator is still running.
type cycleCheckingController struct {
	controller.FanController
	cycles  <-chan struct{}
	clock   *util.FakeClock
	started chan struct{}
	// result: true if the cycles arrived after the context was done
	cycleAfterStop chan bool
}

func (c *cycleCheckingController) SetCurveCycles(cycles <-chan struct{}) { c.cycles = cycles }
func (c *cycleCheckingController) GetFanId() string                      { return "fan" }

func (c *cycleCheckingController) Run(ctx context.Context) error {
	close(c.started)
	<-ctx.Done()
	// drop the notification of a cycle before the stop
	select {
	case <-c.cycles:
	default:
	}
	timeout := time.After(5 * time.Second)
	for range cyclesAfterStop {
		// the next tick of the evaluator
		c.clock.Advance(evaluatorTestTickRate)
		select {
		case <-c.cycles:
		case <-timeout:
			c.cycleAfterStop <- false
			return nil
		}
	}
	c.cycleAfterStop <- true
	return nil
}

// On reload (and exit), the curve evaluator is stopped only after the fan controllers, which it drives.
func TestRunOrchestratorCycle_StopsCurveEvaluatorAfterFanControllers(t *testing.T) {
	originalConfig := configuration.CurrentConfig
	t.Cleanup(func() { configuration.CurrentConfig = originalConfig })
	configuration.CurrentConfig = configuration.Configuration{TempSensorPollingRate: time.Hour}

	clock := util.NewFakeClock(time.Unix(1000, 0))
	sensor := &sensors.HwmonSensor{Config: configuration.SensorConfig{ID: "sensor"}}
	sensor.RecordReading(sensors.Reading{Value: 50000, Time: clock.Now()})
	sensor.SetMovingAvg(50000)
	reg := registry.NewRegistry()
	reg.RegisterSensor(sensor)
	curve, err := curves.NewSpeedCurve(configuration.CurveConfig{ID: "curve", Linear: &configuration.LinearCurveConfig{Sensor: "sensor", Min: 40, Max: 80}})
	require.NoError(t, err)
	reg.RegisterCurve(curve)
	evaluator, err := newCurveEvaluator(reg, evaluatorTestTickRate)
	require.NoError(t, err)
	evaluator.SetClock(clock)

	fan, err := fans.NewFan(configuration.FanConfig{ID: "fan", Curve: "curve", File: &configuration.FileFanConfig{Path: "/nonexistent/fan2go-test/pwm"}})
	require.NoError(t, err)
	fanController := &cycleCheckingController{clock: clock, started: make(chan struct{}), cycleAfterStop: make(chan bool, 1)}
	fanController.SetCurveCycles(evaluator.Subscribe())

	reloadChan := make(chan struct{}, 1)
	done := make(chan bool, 1)
	go func() {
		done <- runOrchestratorCycle(context.Background(), reloadChan, reg, map[fans.Fan]controller.FanController{fan: fanController}, evaluator)
	}()
	<-fanController.started
	// the evaluator has created its ticker and run its first cycle
	require.Eventually(t, func() bool { return curve.CurrentResult().Err == nil }, 5*time.Second, time.Millisecond)

	// WHEN: a reload is requested
	reloadChan <- struct{}{}

	// THEN: the evaluator was still running while the controller stopped
	assert.True(t, <-fanController.cycleAfterStop, "no evaluator cycle after the controller was cancelled")
	select {
	case appExiting := <-done:
		assert.False(t, appExiting)
	case <-time.After(5 * time.Second):
		t.Fatal("runOrchestratorCycle did not return")
	}
	// ... and the curve has been evaluated
	assert.NoError(t, curve.CurrentResult().Err)
	assert.InDelta(t, 255*0.25, curve.CurrentValue(), 0.001)
}

// The curve evaluator runs until its context is done.
func TestStartCurveEvaluator_StopsWhenCancelled(t *testing.T) {
	evaluator, err := curves.NewCurveEvaluator(nil, time.Second)
	require.NoError(t, err)
	evaluator.SetClock(util.NewFakeClock(time.Unix(1000, 0)))
	cycles := evaluator.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	startCurveEvaluator(ctx, evaluator, &wg)
	<-cycles
	cancel()

	stopped := make(chan struct{})
	go func() { wg.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("curve evaluator did not stop")
	}
}
