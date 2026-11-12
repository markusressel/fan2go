package statistics

import (
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/prometheus/client_golang/prometheus"
)

const fanSubsystem = "fan"

// fanCollectorEntry is a fan together with the controller that controls it
type fanCollectorEntry struct {
	fan        fans.Fan
	controller controller.FanController
	// whether the fan has an RPM sensor, determined once when the collector is created
	supportsRpm bool
}

// FanCollector exports the state of the fans as it is maintained by fan2go: the PWM value
// last set by the fan controller and the RPM moving average maintained by its RPM monitor.
// It doesn't access the fan hardware, so a scrape doesn't trigger any hardware reads.
type FanCollector struct {
	fans []fanCollectorEntry
	pwm  *prometheus.Desc
	rpm  *prometheus.Desc
}

func NewFanCollector(fanControllers map[fans.Fan]controller.FanController) *FanCollector {
	var entries []fanCollectorEntry
	for fan, fanController := range fanControllers {
		entries = append(entries, fanCollectorEntry{
			fan:         fan,
			controller:  fanController,
			supportsRpm: fan.Supports(fans.FeatureRpmSensor),
		})
	}
	return &FanCollector{
		fans: entries,
		pwm: prometheus.NewDesc(prometheus.BuildFQName(namespace, fanSubsystem, "pwm"),
			"PWM value last set to the fan by fan2go (raw value, after applying the pwmMap)",
			[]string{"id"}, nil,
		),
		rpm: prometheus.NewDesc(prometheus.BuildFQName(namespace, fanSubsystem, "rpm"),
			"Current RPM value of the fan (moving average)",
			[]string{"id"}, nil,
		),
	}
}

func (collector *FanCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.pwm
	ch <- collector.rpm
}

// Collect implements required collect function for all prometheus collectors
func (collector *FanCollector) Collect(ch chan<- prometheus.Metric) {
	for _, entry := range collector.fans {
		fanId := entry.fan.GetId()

		// the pwm value is only known once the controller has set one
		if pwm, ok := entry.controller.GetLastSetPwm(); ok {
			ch <- prometheus.MustNewConstMetric(collector.pwm, prometheus.GaugeValue, float64(pwm), fanId)
		}

		// the rpm average is only known once the controller's RPM monitor has seeded it
		if entry.supportsRpm {
			if rpm, ok := entry.controller.GetRpmAvg(); ok {
				ch <- prometheus.MustNewConstMetric(collector.rpm, prometheus.GaugeValue, rpm, fanId)
			}
		}
	}
}
