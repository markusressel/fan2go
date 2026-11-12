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

	unexpectedPwmValueCount *prometheus.Desc
	increasedMinPwmCount    *prometheus.Desc
	minPwmOffset            *prometheus.Desc
	state                   *prometheus.Desc
}

func NewControllerCollector(controllers []controller.FanController) *ControllerCollector {
	return &ControllerCollector{
		controllers: controllers,
		unexpectedPwmValueCount: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "unexpected_pwm_value_count"),
			"Counter for instances of a mismatch between expected PWM value and actual PWM value of for this controller",
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
		state: prometheus.NewDesc(prometheus.BuildFQName(namespace, controllerSubsystem, "state"),
			"State of the fan controller: 1 for the current state, 0 for all others ("+allStatesText()+")",
			[]string{"id", "state"}, nil,
		),
	}
}

func (collector *ControllerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.unexpectedPwmValueCount
	ch <- collector.increasedMinPwmCount
	ch <- collector.minPwmOffset
	ch <- collector.state
}

// Collect implements required collect function for all prometheus collectors
func (collector *ControllerCollector) Collect(ch chan<- prometheus.Metric) {
	for _, contr := range collector.controllers {
		switch contr.(type) {
		case *controller.DefaultFanController:
			fanId := contr.GetFanId()
			ch <- prometheus.MustNewConstMetric(collector.unexpectedPwmValueCount, prometheus.CounterValue, float64(contr.GetStatistics().UnexpectedPwmValueCount), fanId)
			ch <- prometheus.MustNewConstMetric(collector.increasedMinPwmCount, prometheus.CounterValue, float64(contr.GetStatistics().IncreasedMinPwmCount), fanId)
			ch <- prometheus.MustNewConstMetric(collector.minPwmOffset, prometheus.GaugeValue, float64(contr.GetStatistics().MinPwmOffset), fanId)
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
