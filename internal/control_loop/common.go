package control_loop

// ControlLoop defines how a FanController approaches the target value of its curve.
type ControlLoop interface {
	// Cycle advances the control loop to the next step and returns the new pwm value.
	// Note: multiple calls to Loop may not result in the same output, as
	// the control loop may take time into account or have other stateful properties.
	Cycle(target int) int
}
