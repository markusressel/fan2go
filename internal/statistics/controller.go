package statistics

import (
	"strings"

	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/controller/status"
	"github.com/prometheus/client_golang/prometheus"
)

const controllerSubsystem = "controller"

type ControllerCollector struct {
	controllers []controller.FanController

	// read glitches: mismatches of the third party sanity checks that a second read didn't confirm
	pwmReadGlitchCount         *prometheus.Desc
	controlModeReadGlitchCount *prometheus.Desc

	unexpectedPwmValueCount *prometheus.Desc
	increasedMinPwmCount    *prometheus.Desc
	minPwmOffset            *prometheus.Desc
	stallCount              *prometheus.Desc
	kickStartCount          *prometheus.Desc
	failedKickStartCount    *prometheus.Desc
	kickStartPwm            *prometheus.Desc
	state                   *prometheus.Desc
}

func NewControllerCollector(controllers []controller.FanController) *ControllerCollector {
	return &ControllerCollector{
		controllers: controllers,
		unexpectedPwmValueCount: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "unexpected_pwm_value_count"),
			"Counter for instances of a mismatch between expected PWM value and actual PWM value of for this controller",
			[]string{"id"}, nil,
		),
		pwmReadGlitchCount: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "pwm_read_glitch_count"),
			"Counter for unexpected PWM values of this controller's fan that a second read didn't confirm (read glitches, not counted as unexpected_pwm_value_count)",
			[]string{"id"}, nil,
		),
		controlModeReadGlitchCount: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "control_mode_read_glitch_count"),
			"Counter for unexpected control modes of this controller's fan that a second read didn't confirm (read glitches)",
			[]string{"id"}, nil,
		),
		increasedMinPwmCount: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "increased_minPwm_count"),
			"Counter for number of automatic increases of the minPwm value due to a stalling fan",
			[]string{"id"}, nil,
		),
		minPwmOffset: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "minPwm_offset"),
			"Offset applied to the original minPwm of the fan due to a stalling fan",
			[]string{"id"}, nil,
		),
		stallCount: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "stall_count"),
			"Counter for stalls of a fan without neverStop: the fan didn't spin although its PWM value was at least its minPwm",
			[]string{"id"}, nil,
		),
		kickStartCount: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "kick_start_count"),
			"Counter for kicks that should start the fan from standstill (each attempt counts)",
			[]string{"id"}, nil,
		),
		failedKickStartCount: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "failed_kick_start_count"),
			"Counter for kicks after which the RPM sensor didn't show the fan spinning",
			[]string{"id"}, nil,
		),
		kickStartPwm: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "kick_start_pwm"),
			"PWM value (before the pwmMap) of the last kick that should start the fan from standstill, 0 if there was none",
			[]string{"id"}, nil,
		),
		state: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "state"),
			"State of the fan controller: 1 for the current state, 0 for all others ("+allStatesText()+")",
			[]string{"id", "state"}, nil,
		),
	}
}

func (collector *ControllerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.unexpectedPwmValueCount
	ch <- collector.pwmReadGlitchCount
	ch <- collector.controlModeReadGlitchCount
	ch <- collector.increasedMinPwmCount
	ch <- collector.minPwmOffset
	ch <- collector.stallCount
	ch <- collector.kickStartCount
	ch <- collector.failedKickStartCount
	ch <- collector.kickStartPwm
	ch <- collector.state
}

// Collect implements required collect function for all prometheus collectors
func (collector *ControllerCollector) Collect(ch chan<- prometheus.Metric) {
	for _, contr := range collector.controllers {
		switch contr.(type) {
		case *controller.DefaultFanController:
			fanId := contr.GetFanId()
			stats := contr.GetStatistics()
			ch <- prometheus.MustNewConstMetric(collector.unexpectedPwmValueCount, prometheus.CounterValue, float64(stats.UnexpectedPwmValueCount), fanId)
			ch <- prometheus.MustNewConstMetric(collector.pwmReadGlitchCount, prometheus.CounterValue, float64(stats.PwmReadGlitchCount), fanId)
			ch <- prometheus.MustNewConstMetric(collector.controlModeReadGlitchCount, prometheus.CounterValue, float64(stats.ControlModeReadGlitchCount), fanId)
			ch <- prometheus.MustNewConstMetric(collector.increasedMinPwmCount, prometheus.CounterValue, float64(stats.IncreasedMinPwmCount), fanId)
			ch <- prometheus.MustNewConstMetric(collector.minPwmOffset, prometheus.GaugeValue, float64(stats.MinPwmOffset), fanId)
			ch <- prometheus.MustNewConstMetric(collector.stallCount, prometheus.CounterValue, float64(stats.StallCount), fanId)
			ch <- prometheus.MustNewConstMetric(collector.kickStartCount, prometheus.CounterValue, float64(stats.KickStartCount), fanId)
			ch <- prometheus.MustNewConstMetric(collector.failedKickStartCount, prometheus.CounterValue, float64(stats.FailedKickStartCount), fanId)
			ch <- prometheus.MustNewConstMetric(collector.kickStartPwm, prometheus.GaugeValue, float64(stats.KickStartPwm), fanId)
		}
		current := contr.GetStatus().State
		for _, state := range status.AllStates {
			ch <- prometheus.MustNewConstMetric(collector.state, prometheus.GaugeValue, boolToFloat(state == current), contr.GetFanId(), string(state))
		}
	}
}

// allStatesText returns all controller states, separated by commas.
func allStatesText() string {
	var states []string
	for _, state := range status.AllStates {
		states = append(states, string(state))
	}
	return strings.Join(states, ", ")
}
