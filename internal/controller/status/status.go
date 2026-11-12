// Package status contains the state of a fan controller, as exported by the statistics and the REST API.
// It has no dependencies, so the registry can use it without importing the controller package.
package status

import (
	"time"
)

// State is the state of a fan controller, as exported by the statistics and the REST API.
type State string

const (
	// StateStopped: the controller hasn't been started yet, or it stopped cleanly (shutdown or config reload)
	StateStopped State = "stopped"
	// StateInitializing: Run started, but the controller doesn't control the fan yet
	// (sensor warm-up, fan initialization, PWM map detection)
	StateInitializing State = "initializing"
	// StateRunning: the controller controls the fan according to its curve
	StateRunning State = "running"
	// StateSensorFailureMax: the curve can't be evaluated, the fan is held at its max PWM
	// (onSensorFailure: max, or restore that couldn't hand the fan over)
	StateSensorFailureMax State = "sensor_failure_max"
	// StateSensorFailureRestored: the curve can't be evaluated, the fan was handed back (onSensorFailure: restore)
	StateSensorFailureRestored State = "sensor_failure_restored"
	// StateStoppedPwmWriteFailed: the controller stopped, because setting the PWM value kept failing
	StateStoppedPwmWriteFailed State = "stopped_pwm_write_failed"
	// StateStoppedStalled: the controller stopped, because the fan (neverStop) didn't spin even at its max PWM
	StateStoppedStalled State = "stopped_stalled"
	// StateStoppedError: the controller stopped because of another error (e.g. during the initialization)
	StateStoppedError State = "stopped_error"
)

// AllStates contains all states a fan controller can be in.
var AllStates = []State{
	StateStopped,
	StateInitializing,
	StateRunning,
	StateSensorFailureMax,
	StateSensorFailureRestored,
	StateStoppedPwmWriteFailed,
	StateStoppedStalled,
	StateStoppedError,
}

// IsStopped returns true if the controller doesn't run (anymore), for whatever reason.
func (s State) IsStopped() bool {
	switch s {
	case StateStopped, StateStoppedPwmWriteFailed, StateStoppedStalled, StateStoppedError:
		return true
	}
	return false
}

// Status is a snapshot of the state of a fan controller.
type Status struct {
	// FanId is the id of the controlled fan
	FanId string `json:"fanId"`
	// State is the current state of the controller
	State State `json:"state"`
	// Since is the time the controller entered State, zero if it has never been started
	Since time.Time `json:"since,omitzero"`
	// Error describes why the controller is in a failure state or stopped, empty otherwise
	Error string `json:"error,omitempty"`
}
