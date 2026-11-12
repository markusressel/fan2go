package internal

import (
	"math"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const factoryTestTick = 200 * time.Millisecond

var factoryTestStart = time.Unix(1000, 0)

// stepResponse cycles the loop at 100, then at 200 one tick later, and returns the second output.
func stepResponse(loop control_loop.ControlLoop) float64 {
	loop.Cycle(factoryTestStart, 100)
	return loop.Cycle(factoryTestStart.Add(factoryTestTick), 200)
}

func TestCreateControlLoop_DefaultIsDirectWithTimeConstantAndDeadband(t *testing.T) {
	// WHEN: a fan without controlAlgorithm
	loop := createControlLoop(configuration.FanConfig{ID: "fan"}, factoryTestTick)

	// THEN: direct with a low-pass of 2s
	require.IsType(t, &control_loop.DirectControlLoop{}, loop)
	assert.InDelta(t, 100+100*(1-math.Exp(-0.1)), stepResponse(loop), 1e-9)

	// THEN: changes below the deadband of 2.55 are not applied
	loop = createControlLoop(configuration.FanConfig{ID: "fan"}, factoryTestTick)
	loop.Cycle(factoryTestStart, 100)
	assert.Equal(t, 100.0, loop.Cycle(factoryTestStart.Add(time.Minute), 102.5))
}

func TestCreateControlLoop_ExplicitDirectIsAPassThrough(t *testing.T) {
	loop := createControlLoop(configuration.FanConfig{
		ID:               "fan",
		ControlAlgorithm: &configuration.ControlAlgorithmConfig{Direct: &configuration.DirectControlAlgorithmConfig{}},
	}, factoryTestTick)

	assert.Equal(t, 200.0, stepResponse(loop))
}

func TestCreateControlLoop_DirectOptions(t *testing.T) {
	loop := createControlLoop(configuration.FanConfig{
		ID: "fan",
		ControlAlgorithm: &configuration.ControlAlgorithmConfig{Direct: &configuration.DirectControlAlgorithmConfig{
			RampUp: 25.5,
		}},
	}, factoryTestTick)

	// THEN: 25.5 per second
	assert.InDelta(t, 105.1, stepResponse(loop), 1e-9)
}

func TestCreateControlLoop_MaxPwmChangePerCycle(t *testing.T) {
	maxChange := 10
	loop := createControlLoop(configuration.FanConfig{
		ID: "fan",
		ControlAlgorithm: &configuration.ControlAlgorithmConfig{Direct: &configuration.DirectControlAlgorithmConfig{
			MaxPwmChangePerCycle: &maxChange, //nolint:staticcheck // deprecated, but still supported
		}},
	}, factoryTestTick)

	assert.Equal(t, 110.0, stepResponse(loop))
}

func TestCreateControlLoop_Pid(t *testing.T) {
	for name, config := range map[string]configuration.FanConfig{
		"controlAlgorithm.pid": {ID: "fan", ControlAlgorithm: &configuration.ControlAlgorithmConfig{
			Pid: &configuration.PidControlAlgorithmConfig{P: 0.05, I: 0.4, D: 0.01},
		}},
		"deprecated controlLoop": {ID: "fan", ControlLoop: &configuration.ControlLoopConfig{P: 0.05, I: 0.4, D: 0.01}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.IsType(t, &control_loop.PidControlLoop{}, createControlLoop(config, factoryTestTick))
		})
	}
}
