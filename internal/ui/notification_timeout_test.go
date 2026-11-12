package ui

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNotificationCommand_HangingCommand_IsKilledAfterTimeout(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skipf("sh not found: %v", err)
	}
	// GIVEN: a command that hangs, and a process it started keeps its output open
	cmd, cancel := notificationCommand(200*time.Millisecond, "sh", "-c", "sleep 5 & wait")
	defer cancel()

	// WHEN
	start := time.Now()
	_, err := cmd.Output()

	// THEN: the call returns after the timeout and the wait delay for the output
	assert.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second)
}
