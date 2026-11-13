package persistence

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireProcessLock_HeldByAnother_ReturnsTheHolder(t *testing.T) {
	// GIVEN
	dbPath := filepath.Join(t.TempDir(), "fan2go.db")
	release, err := AcquireProcessLock(dbPath)
	require.NoError(t, err)

	// WHEN: a second lock (flock locks belong to the open file, so this conflicts within one process, too)
	_, err = AcquireProcessLock(dbPath)

	// THEN
	var locked *ProcessLockedError
	require.True(t, errors.As(err, &locked), "got %v", err)
	assert.Equal(t, os.Getpid(), locked.Pid)
	assert.Equal(t, ProcessLockPath(dbPath), locked.Path)
	assert.Contains(t, err.Error(), "uses the fan2go database")

	// WHEN released
	release()
	release2, err := AcquireProcessLock(dbPath)

	// THEN
	require.NoError(t, err)
	release2()
}

func TestAcquireProcessLock_StaleLockFile_IsIgnored(t *testing.T) {
	// GIVEN: a lock file left behind by a process that was killed (the kernel released its lock)
	dbPath := filepath.Join(t.TempDir(), "fan2go.db")
	require.NoError(t, os.WriteFile(ProcessLockPath(dbPath), []byte("999999\n"), 0644))

	// WHEN
	release, err := AcquireProcessLock(dbPath)

	// THEN: locked, with the own PID
	require.NoError(t, err)
	defer release()
	content, err := os.ReadFile(ProcessLockPath(dbPath))
	require.NoError(t, err)
	assert.Equal(t, []byte(strconv.Itoa(os.Getpid())+"\n"), content)
}

func TestAcquireProcessLock_MissingDirectory_ReturnsAnError(t *testing.T) {
	_, err := AcquireProcessLock(filepath.Join(t.TempDir(), "missing", "fan2go.db"))

	assert.Error(t, err)
	var locked *ProcessLockedError
	assert.False(t, errors.As(err, &locked))
}

func TestAcquireProcessLockShared_NoLockFile_IsANoOp(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fan2go.db")

	release, err := AcquireProcessLockShared(dbPath)

	require.NoError(t, err)
	release()
	assert.NoFileExists(t, ProcessLockPath(dbPath))
}

func TestAcquireProcessLockShared_ExclusiveHeld_ReturnsTheHolder(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fan2go.db")
	release, err := AcquireProcessLock(dbPath)
	require.NoError(t, err)
	defer release()

	_, err = AcquireProcessLockShared(dbPath)

	var locked *ProcessLockedError
	require.True(t, errors.As(err, &locked), "got %v", err)
	assert.Equal(t, os.Getpid(), locked.Pid)
}

func TestAcquireProcessLockShared_Held_BlocksTheExclusiveLock(t *testing.T) {
	// GIVEN: a lock file from an earlier run, and a shared lock on it
	dbPath := filepath.Join(t.TempDir(), "fan2go.db")
	require.NoError(t, os.WriteFile(ProcessLockPath(dbPath), []byte("999999\n"), 0644))
	release, err := AcquireProcessLockShared(dbPath)
	require.NoError(t, err)

	// WHEN
	_, err = AcquireProcessLock(dbPath)

	// THEN: the daemon can't start until the shared lock is released
	var locked *ProcessLockedError
	assert.True(t, errors.As(err, &locked), "got %v", err)
	release()
	releaseExclusive, err := AcquireProcessLock(dbPath)
	require.NoError(t, err)
	releaseExclusive()
}
