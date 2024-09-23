package control_loop

type ControlLoop interface {
	// Loop advances the control loop
	Loop(target float64, measured float64) float64
}
