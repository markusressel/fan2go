package statistics

import (
	"testing"

	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registeredSensorIds returns the ids of the sensors exported by the default Prometheus registry.
func registeredSensorIds(t *testing.T) []string {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	var ids []string
	for _, family := range families {
		if family.GetName() != "fan2go_sensor_read_failures_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "id" {
					ids = append(ids, label.GetValue())
				}
			}
		}
	}
	return ids
}

func sensorCollector(id string) prometheus.Collector {
	return NewSensorCollector([]sensors.Sensor{&fakeSensor{id: id}})
}

func TestReplaceAll_ReplacesTheCollectors(t *testing.T) {
	t.Cleanup(UnregisterAll)

	require.NoError(t, ReplaceAll([]prometheus.Collector{sensorCollector("old")}))
	assert.Equal(t, []string{"old"}, registeredSensorIds(t))

	require.NoError(t, ReplaceAll([]prometheus.Collector{sensorCollector("new")}))
	assert.Equal(t, []string{"new"}, registeredSensorIds(t))
}

func TestReplaceAll_KeepsThePreviousCollectorsOnError(t *testing.T) {
	t.Cleanup(UnregisterAll)
	require.NoError(t, ReplaceAll([]prometheus.Collector{sensorCollector("old")}))

	// WHEN: the new collectors can't be registered (two collectors with the same metrics)
	err := ReplaceAll([]prometheus.Collector{sensorCollector("new"), sensorCollector("duplicate")})

	// THEN: the previous collectors are still registered, and none of the new ones
	assert.Error(t, err)
	assert.Equal(t, []string{"old"}, registeredSensorIds(t))

	// ... and they are still replaced (unregistered) by the next successful call
	require.NoError(t, ReplaceAll([]prometheus.Collector{sensorCollector("next")}))
	assert.Equal(t, []string{"next"}, registeredSensorIds(t))
}
