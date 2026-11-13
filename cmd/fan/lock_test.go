package fan

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenPersistenceExclusively_DaemonRunning_Refuses(t *testing.T) {
	// GIVEN: the daemon holds the lock of the database
	dbPath := filepath.Join(t.TempDir(), "fan2go.db")
	release, err := persistence.AcquireProcessLock(dbPath)
	require.NoError(t, err)
	defer release()

	// WHEN
	_, _, err = openPersistenceExclusively(dbPath)

	// THEN
	var locked *persistence.ProcessLockedError
	assert.True(t, errors.As(err, &locked))
	assert.ErrorContains(t, err, "Stop it first")
}

func TestOpenPersistenceExclusively_FreshInstall_CreatesTheDirectory(t *testing.T) {
	// GIVEN: the database directory doesn't exist yet
	dbPath := filepath.Join(t.TempDir(), "var", "lib", "fan2go", "fan2go.db")

	// WHEN
	p, release, err := openPersistenceExclusively(dbPath)

	// THEN: the data can be deleted (fan reset on a system where the daemon never ran)
	require.NoError(t, err)
	defer release()
	assert.NoError(t, p.DeleteFanPwmMap("fan"))
}
