package ui

import (
	"fmt"
	"runtime"
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

// notificationTestTimeout is the upper bound for things that are expected to happen right away
const notificationTestTimeout = 5 * time.Second

// fakeNotificationEnv replaces the session lookup, the sending and the worker's wait for a test
type fakeNotificationEnv struct {
	mu       sync.Mutex
	sessions []displaySession
	sent     []string
	// tick makes the worker look for sessions again
	tick chan time.Time
}

func newFakeNotificationEnv(t *testing.T) *fakeNotificationEnv {
	env := &fakeNotificationEnv{tick: make(chan time.Time)}
	origGetSessions := getDisplaySessions
	origSend := sendToSession
	origWait := workerWait
	t.Cleanup(func() {
		// stop the worker before restoring the functions it uses
		pendingMu.Lock()
		pendingNotifications = nil
		pendingMu.Unlock()
		env.waitForWorkerStop(t)
		getDisplaySessions = origGetSessions
		sendToSession = origSend
		workerWait = origWait
	})

	getDisplaySessions = func() []displaySession {
		env.mu.Lock()
		defer env.mu.Unlock()
		return env.sessions
	}
	sendToSession = func(session displaySession, urgency, title, text, icon string) {
		env.mu.Lock()
		defer env.mu.Unlock()
		env.sent = append(env.sent, fmt.Sprintf("%s:%s:%s", session.user, title, text))
	}
	workerWait = func() <-chan time.Time { return env.tick }

	pendingMu.Lock()
	pendingNotifications = nil
	workerStarted = false
	pendingMu.Unlock()
	return env
}

func (env *fakeNotificationEnv) setSessions(sessions ...displaySession) {
	env.mu.Lock()
	defer env.mu.Unlock()
	env.sessions = sessions
}

func (env *fakeNotificationEnv) sentNotifications() []string {
	env.mu.Lock()
	defer env.mu.Unlock()
	return append([]string(nil), env.sent...)
}

// tickWorker lets the worker run one round
func (env *fakeNotificationEnv) tickWorker(t *testing.T) {
	t.Helper()
	select {
	case env.tick <- time.Time{}:
	case <-time.After(notificationTestTimeout):
		t.Fatal("notification worker doesn't wait for the next round")
	}
}

func workerRunning() bool {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	return workerStarted
}

// waitForWorkerStop waits until the worker has stopped, letting it run rounds
func (env *fakeNotificationEnv) waitForWorkerStop(t *testing.T) {
	t.Helper()
	deadline := time.After(notificationTestTimeout)
	for workerRunning() {
		select {
		case env.tick <- time.Time{}:
		case <-deadline:
			t.Fatal("notification worker didn't stop")
		default:
			runtime.Gosched()
		}
	}
}

func TestNotifySend_QueuesAndFlushes(t *testing.T) {
	// GIVEN
	env := newFakeNotificationEnv(t)

	// WHEN: no sessions exist
	NotifyWarn("Queued Title", "Queued Msg")

	// THEN: it should be queued, and worker should start
	pendingMu.Lock()
	assert.Len(t, pendingNotifications, 1)
	assert.True(t, workerStarted)
	pendingMu.Unlock()

	// WHEN: the worker looks again, still without a session
	env.tickWorker(t)

	// THEN: nothing is sent yet
	assert.Empty(t, env.sentNotifications())

	// WHEN: graphical session starts
	env.setSessions(displaySession{user: "sessionUser", display: ":0"})
	env.waitForWorkerStop(t)

	// THEN: notification is sent to the active session, and the queue is empty
	assert.Equal(t, []string{"sessionUser:Queued Title:Queued Msg"}, env.sentNotifications())
	pendingMu.Lock()
	assert.Empty(t, pendingNotifications)
	pendingMu.Unlock()
}

func TestNotificationWorker_DoesNotHoldLockWhileSending(t *testing.T) {
	// GIVEN: a queued notification, and sending blocks until released
	env := newFakeNotificationEnv(t)
	sending := make(chan struct{})
	release := make(chan struct{})
	sendToSession = func(session displaySession, urgency, title, text, icon string) {
		close(sending)
		<-release
	}
	NotifyError("Title", "Msg")

	// WHEN: a session appears and the worker sends to it
	env.setSessions(displaySession{user: "user", display: ":0"})
	env.tickWorker(t)
	select {
	case <-sending:
	case <-time.After(notificationTestTimeout):
		t.Fatal("notification worker didn't send")
	}

	// THEN: queueing another notification doesn't block meanwhile
	env.setSessions()
	queued := make(chan struct{})
	go func() {
		NotifyError("Other Title", "Other Msg")
		close(queued)
	}()
	select {
	case <-queued:
	case <-time.After(notificationTestTimeout):
		t.Fatal("NotifySend blocked while the worker was sending")
	}

	// AND: the worker keeps running for the notification queued while it was sending
	close(release)
	pendingMu.Lock()
	assert.Len(t, pendingNotifications, 1)
	pendingMu.Unlock()
	env.tickWorker(t)
	assert.True(t, workerRunning())
}

func TestQueueNotification_DropsOldestBeyondLimit(t *testing.T) {
	// GIVEN
	newFakeNotificationEnv(t)

	// WHEN: more notifications are queued than the limit
	pendingMu.Lock()
	defer pendingMu.Unlock()
	for i := 0; i < maxPendingNotifications+5; i++ {
		queueNotification(pendingNotification{title: fmt.Sprintf("n%d", i)})
	}

	// THEN: the newest are kept
	assert.Len(t, pendingNotifications, maxPendingNotifications)
	assert.Equal(t, "n5", pendingNotifications[0].title)
	assert.Equal(t, fmt.Sprintf("n%d", maxPendingNotifications+4), pendingNotifications[maxPendingNotifications-1].title)
}
