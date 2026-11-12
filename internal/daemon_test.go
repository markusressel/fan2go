package internal

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReloadConfiguration_InvalidConfigKeepsCurrentConfig(t *testing.T) {
	// GIVEN a config file that loads, but fails validation
	// (adjustmentTickRate 0 would make time.NewTicker panic in the fan controller)
	configPath := filepath.Join(t.TempDir(), "fan2go.yaml")
	err := os.WriteFile(configPath, []byte(`
fanController:
  adjustmentTickRate: 0s
sensors:
  - id: sensor
    file:
      path: /dev/null
curves:
  - id: curve
    linear:
      sensor: sensor
      steps:
        - 40: 0
        - 80: 255
fans:
  - id: fan
    curve: curve
    file:
      path: /dev/null
`), 0o600)
	require.NoError(t, err)

	viper.Reset()
	t.Cleanup(viper.Reset)
	configuration.InitConfig(configPath)

	oldConfig := configuration.CurrentConfig
	t.Cleanup(func() { configuration.CurrentConfig = oldConfig })
	configuration.CurrentConfig = configuration.Configuration{DbPath: "current"}
	configuration.CurrentConfig.FanController.AdjustmentTickRate = 200 * time.Millisecond

	// WHEN
	reg, controllers, evaluator, err := reloadConfiguration(nil)

	// THEN
	assert.ErrorContains(t, err, "fanController.adjustmentTickRate must be > 0")
	assert.Nil(t, reg)
	assert.Nil(t, controllers)
	assert.Nil(t, evaluator)
	assert.Equal(t, "current", configuration.CurrentConfig.DbPath)
	assert.Equal(t, 200*time.Millisecond, configuration.CurrentConfig.FanController.AdjustmentTickRate)
}
