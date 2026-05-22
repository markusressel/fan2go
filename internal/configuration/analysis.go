package configuration

import "time"

type AnalysisConfig struct {
	// CoarseStep is the step size for the coarse sweep during fan initialization.
	// Every Nth distinct PWM value is measured in Phase 1. Default: 16.
	CoarseStep int `json:"coarseStep"`
	// SampleCount is the number of RPM samples taken at each PWM point. Default: 3.
	SampleCount int `json:"sampleCount"`
	// SampleDelay is the delay between consecutive RPM samples. Default: 200ms.
	SampleDelay time.Duration `json:"sampleDelay"`
	// SettleTimeout is the maximum time to wait for a fan to settle during initialization.
	// If exceeded, measurement continues with whatever RPM the fan reports. Default: 30s.
	SettleTimeout time.Duration `json:"settleTimeout"`
}
