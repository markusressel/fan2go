package persistence

import (
	"errors"
	berrors "go.etcd.io/bbolt/errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func intPtr(v int) *int { return &v }

func TestPersistence_FanOriginalState_SaveLoadDelete(t *testing.T) {
	// GIVEN
	p, _ := newTempPersistence(t)
	state := FanOriginalState{
		Hardware:             "hwmon:nct6798/0/pwm1",
		ControlMode:          intPtr(2),
		AutomaticControlMode: intPtr(5),
		Pwm:                  intPtr(120),
	}

	// WHEN
	_, err := p.LoadFanOriginalState("cpu")
	// THEN
	assert.ErrorIs(t, err, os.ErrNotExist)

	// WHEN
	require.NoError(t, p.SaveFanOriginalState("cpu", state))
	require.NoError(t, p.SaveFanOriginalState("gpu", FanOriginalState{Hardware: "nvidia:x/0"}))
	loaded, err := p.LoadFanOriginalState("cpu")
	// THEN
	require.NoError(t, err)
	assert.Equal(t, state, loaded)
	all, err := p.LoadFanOriginalStates()
	require.NoError(t, err)
	assert.Equal(t, map[string]FanOriginalState{"cpu": state, "gpu": {Hardware: "nvidia:x/0"}}, all)

	// WHEN
	require.NoError(t, p.DeleteFanOriginalState("cpu"))
	require.NoError(t, p.DeleteFanOriginalState("cpu"), "deleting a missing entry is a no-op")
	// THEN
	_, err = p.LoadFanOriginalState("cpu")
	assert.ErrorIs(t, err, os.ErrNotExist)
	all, err = p.LoadFanOriginalStates()
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestPersistence_FanOriginalState_NoBucket(t *testing.T) {
	p, _ := newTempPersistence(t)

	all, err := p.LoadFanOriginalStates()
	require.NoError(t, err)
	assert.Empty(t, all)
	assert.NoError(t, p.DeleteFanOriginalState("cpu"))
}

func TestPersistence_LoadFanOriginalState_CorruptData(t *testing.T) {
	for name, value := range map[string]string{
		"not json":     "{",
		"invalid pwm":  `{"hardware":"x","controlMode":2,"pwm":300}`,
		"invalid mode": `{"hardware":"x","controlMode":-1,"pwm":10}`,
		"driver mode":  `{"hardware":"x","controlMode":5,"pwm":10}`,
	} {
		t.Run(name, func(t *testing.T) {
			// GIVEN
			p, dbPath := newTempPersistence(t)
			putRaw(t, dbPath, BucketFanOriginalState, "cpu", []byte(value))
			putRaw(t, dbPath, BucketFanOriginalState, "gpu", []byte(`{"hardware":"y","controlMode":null,"pwm":null}`))

			// WHEN
			_, err := p.LoadFanOriginalState("cpu")
			// THEN: reported as corrupt and deleted
			assert.ErrorIs(t, err, ErrCorruptData)
			assert.True(t, IsMissingOrCorrupt(err))
			assert.Nil(t, getRaw(t, dbPath, BucketFanOriginalState, "cpu"))

			// WHEN
			putRaw(t, dbPath, BucketFanOriginalState, "cpu", []byte(value))
			all, err := p.LoadFanOriginalStates()
			// THEN: the corrupt entry is skipped and deleted
			require.NoError(t, err)
			assert.Equal(t, map[string]FanOriginalState{"gpu": {Hardware: "y"}}, all)
			assert.Nil(t, getRaw(t, dbPath, BucketFanOriginalState, "cpu"))
		})
	}
}

func TestPersistence_FanOriginalState_LockedDatabase(t *testing.T) {
	// GIVEN: another process holds the database
	p, dbPath := newTempPersistence(t)
	require.NoError(t, p.SaveFanOriginalState("cpu", FanOriginalState{Hardware: "x"}))
	db, err := bolt.Open(dbPath, 0600, nil)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldTimeout := originalStateDbTimeout
	originalStateDbTimeout = 10 * time.Millisecond
	defer func() { originalStateDbTimeout = oldTimeout }()

	// WHEN
	_, loadErr := p.LoadFanOriginalState("cpu")
	saveErr := p.SaveFanOriginalState("cpu", FanOriginalState{})
	deleteErr := p.DeleteFanOriginalState("cpu")

	// THEN: an error that doesn't mean "missing"
	for _, err := range []error{loadErr, saveErr, deleteErr} {
		assert.Error(t, err)
		assert.False(t, IsMissingOrCorrupt(err))
		assert.True(t, errors.Is(err, berrors.ErrTimeout), "%v", err)
	}
}
