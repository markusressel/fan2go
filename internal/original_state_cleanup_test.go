package internal

import (
	"path/filepath"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForgetOriginalStatesOfUnconfiguredFans_DeletesOnlyStatesOfUnconfiguredFans(t *testing.T) {
	// GIVEN: saved original states of two configured fans and of a removed fan
	// (the hwmon chip of "late" may not have been found yet, it is still configured)
	pers := persistence.NewPersistence(filepath.Join(t.TempDir(), "db", "fan2go.db"))
	require.NoError(t, pers.Init())
	require.NoError(t, pers.SaveFanOriginalState("cpu", persistence.FanOriginalState{Hardware: "file:/cpu"}))
	require.NoError(t, pers.SaveFanOriginalState("late", persistence.FanOriginalState{Hardware: "hwmon:nct6798/0/pwm1"}))
	require.NoError(t, pers.SaveFanOriginalState("removed", persistence.FanOriginalState{Hardware: "file:/removed"}))
	fanConfigs := []configuration.FanConfig{{ID: "cpu"}, {ID: "late"}}

	// WHEN
	forgetOriginalStatesOfUnconfiguredFans(pers, fanConfigs)

	// THEN
	states, err := pers.LoadFanOriginalStates()
	require.NoError(t, err)
	assert.Equal(t, map[string]persistence.FanOriginalState{
		"cpu":  {Hardware: "file:/cpu"},
		"late": {Hardware: "hwmon:nct6798/0/pwm1"},
	}, states)
}

func TestForgetOriginalStatesOfUnconfiguredFans_NoDatabaseYet(t *testing.T) {
	// GIVEN: the first start, the database directory doesn't exist yet
	pers := persistence.NewPersistence(filepath.Join(t.TempDir(), "db", "fan2go.db"))

	// WHEN / THEN: nothing to do, no error
	forgetOriginalStatesOfUnconfiguredFans(pers, nil)
	states, err := pers.LoadFanOriginalStates()
	require.NoError(t, err)
	assert.Empty(t, states)
}
