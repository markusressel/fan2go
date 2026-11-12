package controller

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/ui"
)

// The original fan state (FanStateSnapshot) is also saved in the database while fan2go controls a fan:
// it is saved right before fan2go may write to the fan (markMayWriteToFan), and deleted once fan2go handed the
// fan back completely (forgetOriginalFanState). If fan2go is killed in between, the next run finds the saved
// state and restores the fan to it, instead of to the state the killed run left behind
// (useSavedOriginalFanState). All of this is best effort: database errors are logged, and the controller
// continues with the state it read from the fan.

// originalStateHardwareKey identifies the device a saved original fan state belongs to. It is built from
// the stable config values (not from resolved sysfs paths, which may change between boots), so a saved state
// is not applied to another device if the config of a fan id changes.
func originalStateHardwareKey(cfg configuration.FanConfig) string {
	switch {
	case cfg.HwMon != nil:
		return fmt.Sprintf("hwmon:%s/%d/pwm%d", cfg.HwMon.Platform, cfg.HwMon.Index, cfg.HwMon.PwmChannel)
	case cfg.Nvidia != nil:
		return fmt.Sprintf("nvidia:%s/%d", cfg.Nvidia.Device, cfg.Nvidia.Index)
	case cfg.File != nil:
		return "file:" + cfg.File.Path
	case cfg.Cmd != nil && cfg.Cmd.SetPwm != nil:
		return "cmd:" + strings.Join(append([]string{cfg.Cmd.SetPwm.Exec}, cfg.Cmd.SetPwm.Args...), " ")
	default:
		return "unknown"
	}
}

// useSavedOriginalFanState replaces the snapshot that was just read from the fan with the original state
// saved in the database, if there is one for the same hardware: then an earlier run didn't hand the fan back
// (e.g. it was killed), and the fan is still in the state that run left behind.
func (f *DefaultFanController) useSavedOriginalFanState() {
	if f.persistence == nil {
		return
	}
	fan := f.fan
	saved, err := f.persistence.LoadFanOriginalState(fan.GetId())
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
		case errors.Is(err, persistence.ErrCorruptData):
			ui.Warning("Fan %s: Saved original state is corrupt and was deleted, using the current state as original: %v", fan.GetId(), err)
		default:
			// the saved state may exist: don't overwrite or delete it, the next run may be able to use it
			f.originalFanStateUnusable = true
			ui.Warning("Fan %s: Cannot load the saved original state, using the current state as original: %v", fan.GetId(), err)
		}
		return
	}

	current := f.originalFanState
	hardware := originalStateHardwareKey(fan.GetConfig())
	if saved.Hardware != hardware {
		ui.Warning("Fan %s: Ignoring the saved original state (%s) of other hardware (%s, now %s), using the current state as original",
			fan.GetId(), FormatFanOriginalState(saved), saved.Hardware, hardware)
		if err := f.persistence.DeleteFanOriginalState(fan.GetId()); err != nil {
			ui.Warning("Fan %s: Cannot delete the saved original state: %v", fan.GetId(), err)
		}
		return
	}

	ui.Warning("Fan %s: fan2go didn't hand the fan back the last time it ran (killed or crashed?). Using the saved original state (%s) instead of the current state (%s)",
		fan.GetId(), FormatFanOriginalState(saved), FormatFanOriginalState(toFanOriginalState(current, hardware)))
	f.originalFanState = fromFanOriginalState(saved)
	if keeper, ok := fan.(fans.AutomaticControlModeKeeper); ok && saved.AutomaticControlMode != nil &&
		saved.ControlMode != nil && fans.ControlMode(*saved.ControlMode) == fans.ControlModeAutomatic {
		keeper.SetLastKnownAutomaticControlMode(*saved.AutomaticControlMode)
	}
	f.originalFanStateSaved = true
	// the fan is presumably still in the state of the earlier run
	f.handbackPending = true
}

// markMayWriteToFan is called right before fan2go may write to the fan (it takes control of it):
// it sets mayHaveWrittenToFan, and saves the original fan state in the database, unless it is saved already.
func (f *DefaultFanController) markMayWriteToFan() {
	f.mayHaveWrittenToFan = true
	if f.originalFanStateSaved || f.originalFanStateUnusable || f.originalFanStateSaveFailed ||
		f.originalFanState == nil || f.persistence == nil {
		return
	}
	fan := f.fan
	state := toFanOriginalState(f.originalFanState, originalStateHardwareKey(fan.GetConfig()))
	if err := f.persistence.SaveFanOriginalState(fan.GetId(), state); err != nil {
		ui.Warning("Fan %s: Cannot save the original state, it can't be restored if fan2go is killed: %v", fan.GetId(), err)
		f.originalFanStateSaveFailed = true
		return
	}
	f.originalFanStateSaved = true
}

// forgetOriginalFanState deletes the saved original fan state, after fan2go handed the fan back completely.
func (f *DefaultFanController) forgetOriginalFanState() {
	if !f.originalFanStateSaved || f.persistence == nil {
		return
	}
	f.originalFanStateSaved = false
	if err := f.persistence.DeleteFanOriginalState(f.fan.GetId()); err != nil {
		ui.Warning("Fan %s: Cannot delete the saved original state, the next run will use it again: %v", f.fan.GetId(), err)
	}
}

func toFanOriginalState(snapshot *FanStateSnapshot, hardware string) persistence.FanOriginalState {
	state := persistence.FanOriginalState{Hardware: hardware}
	if snapshot == nil {
		return state
	}
	if snapshot.ControlMode != nil {
		mode := int(*snapshot.ControlMode)
		state.ControlMode = &mode
	}
	state.AutomaticControlMode = copyInt(snapshot.AutomaticControlModeValue)
	state.Pwm = copyInt(snapshot.PwmValue)
	return state
}

func fromFanOriginalState(state persistence.FanOriginalState) *FanStateSnapshot {
	snapshot := &FanStateSnapshot{
		PwmValue:                  copyInt(state.Pwm),
		AutomaticControlModeValue: copyInt(state.AutomaticControlMode),
	}
	if state.ControlMode != nil {
		mode := fans.ControlMode(*state.ControlMode)
		snapshot.ControlMode = &mode
	}
	return snapshot
}

// FormatFanOriginalState formats a saved original fan state for log messages.
func FormatFanOriginalState(state persistence.FanOriginalState) string {
	format := func(v *int) string {
		if v == nil {
			return "unknown"
		}
		return strconv.Itoa(*v)
	}
	mode := format(state.ControlMode)
	if state.AutomaticControlMode != nil {
		mode += " (driver value " + format(state.AutomaticControlMode) + ")"
	}
	return "control mode " + mode + ", PWM " + format(state.Pwm)
}

func copyInt(v *int) *int {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}
