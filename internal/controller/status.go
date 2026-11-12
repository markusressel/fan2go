package controller

import (
	"context"
	"errors"

	"github.com/markusressel/fan2go/internal/controller/status"
)

// stoppedStateForError returns the state of a controller that stopped because of err.
func stoppedStateForError(err error) status.State {
	switch {
	case errors.Is(err, ErrPwmWriteFailed):
		return status.StateStoppedPwmWriteFailed
	case errors.Is(err, ErrFanStalledAtMaxPwm):
		return status.StateStoppedStalled
	default:
		return status.StateStoppedError
	}
}

// GetStatus returns the current state of the controller. Safe to call while the controller is running.
func (f *DefaultFanController) GetStatus() status.Status {
	f.statsMutex.Lock()
	defer f.statsMutex.Unlock()
	state := f.state
	if state == "" {
		state = status.StateStopped
	}
	return status.Status{
		FanId: f.fan.GetId(),
		State: state,
		Since: f.stateSince,
		Error: f.stateError,
	}
}

// setState records the state of the controller (err may be nil), if it changed.
func (f *DefaultFanController) setState(state status.State, err error) {
	now := f.getClock().Now()
	f.statsMutex.Lock()
	defer f.statsMutex.Unlock()
	message := ""
	if err != nil {
		message = err.Error()
	}
	if f.state == state && f.stateError == message {
		return
	}
	if f.state != state {
		f.stateSince = now
	}
	f.state = state
	f.stateError = message
}

// getState returns the current state of the controller (status.StateStopped if it has never been started).
func (f *DefaultFanController) getState() status.State {
	return f.GetStatus().State
}

// errHandbackIncomplete is the error of the stopped state after an incomplete restore of the fan settings.
var errHandbackIncomplete = errors.New("fan not handed back completely, see the log")

// setStoppedState records that Run returned err, unless the update loop already recorded why it stopped.
func (f *DefaultFanController) setStoppedState(err error) {
	if f.getState().IsStopped() {
		return
	}
	if err == nil || errors.Is(err, context.Canceled) {
		if f.handbackPending {
			// the last restore was incomplete: the fan may still be in fan2go's state
			f.setState(status.StateStopped, errHandbackIncomplete)
			return
		}
		f.setState(status.StateStopped, nil)
		return
	}
	f.setState(stoppedStateForError(err), err)
}
