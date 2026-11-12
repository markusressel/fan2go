package configuration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectAndReadConfigFile_MissingFileHasHint(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	// GIVEN a config path that doesn't exist
	path := filepath.Join(t.TempDir(), "fan2go.yaml")
	viper.SetConfigFile(path)

	// WHEN
	_, err := DetectAndReadConfigFile()

	// THEN the error is a config error that tells the user how to create the config file
	require.Error(t, err)
	assert.True(t, IsConfigError(err))
	assert.Contains(t, err.Error(), "Create the config file "+path+" ")
	assert.Contains(t, err.Error(), "/usr/share/doc/fan2go/fan2go.yaml.example")
}

func TestDetectAndReadConfigFile_NotFoundInSearchPathsHasHint(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	// GIVEN no config file in the search paths
	viper.SetConfigName("fan2go")
	viper.AddConfigPath(t.TempDir())

	// WHEN
	_, err := DetectAndReadConfigFile()

	// THEN
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Create the config file /etc/fan2go/fan2go.yaml")
}

func TestDetectAndReadConfigFile_InvalidFileHasNoHint(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	// GIVEN a config file that exists but can't be parsed
	path := filepath.Join(t.TempDir(), "fan2go.yaml")
	require.NoError(t, os.WriteFile(path, []byte("fans: [\n"), 0o600))
	viper.SetConfigFile(path)

	// WHEN
	_, err := DetectAndReadConfigFile()

	// THEN
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "fan2go.yaml.example")
}
