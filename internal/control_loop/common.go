package control_loop

import "time"

// ControlLoop defines how a FanController approaches the target value of its curve.
// A ControlLoop is not safe for concurrent use; the update loop of the fan controller owns it.
type ControlLoop interface {
	// Cycle advances the control loop to the time now and returns the new output within [0..255].
	// Note: multiple calls to Cycle may not result in the same output, as
	// the control loop may take time into account or have other stateful properties.
	Cycle(now time.Time, target float64) float64
	// Reset forgets the state of the control loop: the next Cycle starts at its target,
	// like the first cycle of a new control loop.
	Reset()
	// Preset makes the next Cycle continue from value (e.g. the speed the fan has now, after it was set
	// without the control loop), instead of from the last output of the control loop.
	// The value is clamped to [0..255].
	Preset(value float64)
}
