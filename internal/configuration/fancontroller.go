package configuration

import "time"

type FanControllerConfig struct {
	// Time to wait between a set-pwm and get-pwm call. Used to give hardware time to
	// respond to the set-pwm command.
	PwmSetDelay time.Duration `json:"pwmSetDelay"`
	// Time interval between each fan speed update cycle.
	AdjustmentTickRate time.Duration `json:"adjustmentTickRate"`
}
