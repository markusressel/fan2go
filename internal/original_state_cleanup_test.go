package internal

import (
	"path/filepath"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForgetOriginalStatesOfUnconfiguredFans_DeletesOnlyStatesOfUnconfiguredFans(t *testing.T) {
	// GIVEN: saved original states of a configured and of a removed fan
	pers := persistence.NewPersistence(filepath.Join(t.TempDir(), "db", "fan2go.db"))
	require.NoError(t, pers.Init())
	require.NoError(t, pers.SaveFanOriginalState("cpu", persistence.FanOriginalState{Hardware: "file:/cpu"}))
	require.NoError(t, pers.SaveFanOriginalState("removed", persistence.FanOriginalState{Hardware: "file:/removed"}))
	config := configuration.FanConfig{ID: "cpu", File: &configuration.FileFanConfig{Path: "/cpu"}}
	fanMap := map[configuration.FanConfig]fans.Fan{config: &fans.FileFan{Config: config}}

	// WHEN
	forgetOriginalStatesOfUnconfiguredFans(pers, fanMap)

	// THEN
	states, err := pers.LoadFanOriginalStates()
	require.NoError(t, err)
	assert.Equal(t, map[string]persistence.FanOriginalState{"cpu": {Hardware: "file:/cpu"}}, states)
}

func TestForgetOriginalStatesOfUnconfiguredFans_NoDatabaseYet(t *testing.T) {
	// GIVEN: the first start, the database directory doesn't exist yet
	pers := persistence.NewPersistence(filepath.Join(t.TempDir(), "db", "fan2go.db"))

	// WHEN / THEN: nothing to do, no error
	forgetOriginalStatesOfUnconfiguredFans(pers, map[configuration.FanConfig]fans.Fan{})
	states, err := pers.LoadFanOriginalStates()
	require.NoError(t, err)
	assert.Empty(t, states)
}
