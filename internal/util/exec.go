package util

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/markusressel/fan2go/internal/ui"
)

// cmdWaitDelay is how long SafeCmdExecution waits for the output of a command to be closed after
// the command exited or was killed. Processes that left the command's process group (e.g. with
// setsid) and keep its output open would otherwise block the call until they exit.
const cmdWaitDelay = 500 * time.Millisecond

// SafeCmdExecution runs the given executable and returns its trimmed stdout.
// The executable must pass CheckFilePermissionsForExecution.
//
// The command runs in its own process group. When the timeout is reached, the whole process
// group is killed, including processes the command started in the background.
func SafeCmdExecution(executable string, args []string, timeout time.Duration) (string, error) {
	if _, err := CheckFilePermissionsForExecution(executable); err != nil {
		return "", fmt.Errorf("cannot execute %s: %s", executable, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// the process group ID is the PID of the command (Setpgid)
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = cmdWaitDelay

	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			ui.Warning("Command timed out: %s", executable)
			return "", fmt.Errorf("command %s timed out after %s: %w", executable, timeout, err)
		}

		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			ui.Warning("Command failed to execute: %s: %s", executable, string(exitError.Stderr))
			return "", err
		}

		if errors.Is(err, exec.ErrWaitDelay) {
			// the command itself succeeded, but a process it started in the background
			// still holds its output open: use what the command wrote
			ui.Debug("Command %s exited, but a process it started keeps its output open", executable)
		} else {
			// e.g. the file is not executable (anymore)
			ui.Warning("Command failed to execute: %s: %v", executable, err)
			return "", err
		}
	}

	strout := string(out)
	strout = strings.Trim(strout, "\n")

	return strout, nil
}
