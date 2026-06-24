package ui

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNotifySend_ImmediatelySendsIfSessionsExist(t *testing.T) {
	// GIVEN
	origGetSessions := getDisplaySessions
	origSend := sendToSession
	defer func() {
		getDisplaySessions = origGetSessions
		sendToSession = origSend
	}()

	getDisplaySessions = func() []displaySession {
		return []displaySession{
			{user: "user1", display: ":0"},
			{user: "user2", display: ":1"},
		}
	}

	var sent []string
	var mu sync.Mutex
	sendToSession = func(session displaySession, urgency, title, text, icon string) {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, fmt.Sprintf("%s:%s:%s", session.user, title, text))
	}

	pendingMu.Lock()
	pendingNotifications = nil
	workerStarted = false
	pendingMu.Unlock()

	// WHEN
	NotifyWarn("Immediate Title", "Immediate Msg")

	// THEN
	pendingMu.Lock()
	assert.Len(t, pendingNotifications, 0)
	assert.False(t, workerStarted)
	pendingMu.Unlock()

	mu.Lock()
	assert.Len(t, sent, 2)
	assert.Contains(t, sent, "user1:Immediate Title:Immediate Msg")
	assert.Contains(t, sent, "user2:Immediate Title:Immediate Msg")
	mu.Unlock()
}

func TestNotifySend_QueuesAndFlushes(t *testing.T) {
	// GIVEN
	origGetSessions := getDisplaySessions
	origSend := sendToSession
	origInterval := workerPollInterval
	defer func() {
		getDisplaySessions = origGetSessions
		sendToSession = origSend
		workerPollInterval = origInterval
	}()

	workerPollInterval = 50 * time.Millisecond

	var sessions []displaySession
	var getSessionsMu sync.Mutex
	getDisplaySessions = func() []displaySession {
		getSessionsMu.Lock()
		defer getSessionsMu.Unlock()
		return sessions
	}

	var sent []string
	var sendMu sync.Mutex
	sendToSession = func(session displaySession, urgency, title, text, icon string) {
		sendMu.Lock()
		defer sendMu.Unlock()
		sent = append(sent, fmt.Sprintf("%s:%s:%s", session.user, title, text))
	}

	pendingMu.Lock()
	pendingNotifications = nil
	workerStarted = false
	pendingMu.Unlock()

	// WHEN: no sessions exist
	NotifyWarn("Queued Title", "Queued Msg")

	// THEN: it should be queued, and worker should start
	pendingMu.Lock()
	assert.Len(t, pendingNotifications, 1)
	assert.True(t, workerStarted)
	pendingMu.Unlock()

	// WHEN: graphical session starts
	getSessionsMu.Lock()
	sessions = []displaySession{{user: "sessionUser", display: ":0"}}
	getSessionsMu.Unlock()

	// Wait for the background worker to poll and flush
	assert.Eventually(t, func() bool {
		pendingMu.Lock()
		defer pendingMu.Unlock()
		return len(pendingNotifications) == 0 && !workerStarted
	}, 2*time.Second, 100*time.Millisecond)

	// THEN: notification is sent to the active session
	sendMu.Lock()
	assert.Contains(t, sent, "sessionUser:Queued Title:Queued Msg")
	sendMu.Unlock()
}
