package configuration

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadConfig_WebserverHostDefaults(t *testing.T) {
	// GIVEN a config that enables the webservers without setting a host
	cfg := loadConfigFromYaml(t, `
statistics:
  enabled: true
api:
  enabled: true
profiling:
  enabled: true
`)

	// THEN all of them only listen on the loopback interface
	assert.Equal(t, "127.0.0.1", cfg.Statistics.Host)
	assert.Equal(t, 9000, cfg.Statistics.Port)
	assert.Equal(t, "127.0.0.1", cfg.Api.Host)
	assert.Equal(t, "127.0.0.1", cfg.Profiling.Host)
}

func TestLoadConfig_StatisticsHostDefault_WithoutSection(t *testing.T) {
	cfg := loadConfigFromYaml(t, `
dbPath: /tmp/fan2go.db
`)

	assert.Equal(t, "127.0.0.1", cfg.Statistics.Host)
	assert.Equal(t, 9000, cfg.Statistics.Port)
}

func TestLoadConfig_StatisticsHostCanBeSetToAllInterfaces(t *testing.T) {
	cfg := loadConfigFromYaml(t, `
statistics:
  enabled: true
  host: 0.0.0.0
`)

	assert.Equal(t, "0.0.0.0", cfg.Statistics.Host)
}
