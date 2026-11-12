package util

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireRootOwnedExecutable returns the path of the given executable, which has to pass
// CheckFilePermissionsForExecution (owned by root), or skips the test.
func requireRootOwnedExecutable(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not found: %v", name, err)
	}
	if ok, err := CheckFilePermissionsForExecution(path); !ok {
		t.Skipf("%s can't be used by SafeCmdExecution: %v", path, err)
	}
	return path
}

func TestSafeCmdExecution_Success(t *testing.T) {
	sh := requireRootOwnedExecutable(t, "sh")

	out, err := SafeCmdExecution(sh, []string{"-c", "echo 42"}, 2*time.Second)

	require.NoError(t, err)
	assert.Equal(t, "42", out)
}

func TestSafeCmdExecution_NonZeroExit_ReturnsError(t *testing.T) {
	sh := requireRootOwnedExecutable(t, "sh")

	_, err := SafeCmdExecution(sh, []string{"-c", "echo oops >&2; exit 3"}, 2*time.Second)

	var exitError *exec.ExitError
	assert.ErrorAs(t, err, &exitError)
}

func TestSafeCmdExecution_FileNotExecutable_ReturnsErrorInsteadOfPanic(t *testing.T) {
	// GIVEN: a root-owned file that passes the permission check, but has no exec bit,
	// so starting it fails with an *fs.PathError instead of an *exec.ExitError
	const path = "/etc/os-release"
	info, err := os.Stat(path)
	if err != nil {
		t.Skipf("%s not available: %v", path, err)
	}
	if info.Mode()&0o111 != 0 {
		t.Skipf("%s is executable", path)
	}
	if ok, err := CheckFilePermissionsForExecution(path); !ok {
		t.Skipf("%s doesn't pass the permission check: %v", path, err)
	}

	// WHEN
	assert.NotPanics(t, func() {
		_, err = SafeCmdExecution(path, nil, 2*time.Second)
	})

	// THEN
	assert.Error(t, err)
}

func TestSafeCmdExecution_Timeout_KillsBackgroundProcesses(t *testing.T) {
	// GIVEN: a command that waits for a background process, which keeps the command's output open
	sh := requireRootOwnedExecutable(t, "sh")

	// WHEN
	start := time.Now()
	_, err := SafeCmdExecution(sh, []string{"-c", "sleep 5 & wait"}, 200*time.Millisecond)

	// THEN: the whole process group is killed on timeout, so the call returns right away
	assert.ErrorContains(t, err, "timed out")
	assert.Less(t, time.Since(start), 1*time.Second)
}

func TestSafeCmdExecution_Timeout_ProcessOutsideOfProcessGroup_DoesNotBlock(t *testing.T) {
	// GIVEN: a command that starts a process in a new session (outside its process group),
	// which keeps the command's output open and isn't killed with the process group
	sh := requireRootOwnedExecutable(t, "sh")
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skipf("setsid not found: %v", err)
	}

	// WHEN
	start := time.Now()
	_, err := SafeCmdExecution(sh, []string{"-c", "setsid sleep 3 & wait"}, 200*time.Millisecond)

	// THEN: the call returns once the wait delay for the output has expired
	assert.ErrorContains(t, err, "timed out")
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestSafeCmdExecution_BackgroundProcessKeepsOutputOpen_ReturnsOutput(t *testing.T) {
	// GIVEN: a command that prints its result and exits, leaving a background process behind
	// that keeps the command's output open
	sh := requireRootOwnedExecutable(t, "sh")

	// WHEN
	start := time.Now()
	out, err := SafeCmdExecution(sh, []string{"-c", "sleep 3 & echo 42"}, 2*time.Second)

	// THEN: the output of the command is used, without waiting for the background process
	require.NoError(t, err)
	assert.Equal(t, "42", out)
	assert.Less(t, time.Since(start), 2*time.Second)
}
