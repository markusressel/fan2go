package ui

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"sync"
	"time"
)

// For a list of possible icons, see: https://specifications.freedesktop.org/icon-naming-spec/icon-naming-spec-latest.html
const (
	IconDialogError = "dialog-error"
	IconDialogInfo  = "dialog-information"
	IconDialogWarn  = "dialog-warning"

	UrgencyLow      = "low"
	UrgencyNormal   = "normal"
	UrgencyCritical = "critical"
)

type displaySession struct {
	user    string
	display string
}

type pendingNotification struct {
	urgency string
	title   string
	text    string
	icon    string
}

// maxPendingNotifications is the maximum number of notifications that are queued while no graphical session exists.
// When it is exceeded, the oldest ones are dropped (they have been logged when they were raised).
const maxPendingNotifications = 100

var (
	// pendingMu guards pendingNotifications and workerStarted. It is never held while running a command,
	// so NotifySend doesn't block while the worker looks for sessions or sends notifications.
	pendingMu            sync.Mutex
	pendingNotifications []pendingNotification
	workerStarted        bool

	workerPollInterval = 1 * time.Second
	// workerWait returns a channel that fires when the worker should look for a graphical session again,
	// replaceable in tests
	workerWait = func() <-chan time.Time { return time.After(workerPollInterval) }
)

func NotifyInfo(title, text string) {
	NotifySend(UrgencyLow, title, text, IconDialogInfo)
}

func NotifyWarn(title, text string) {
	NotifySend(UrgencyNormal, title, text, IconDialogWarn)
}

func NotifyError(title, text string) {
	NotifySend(UrgencyCritical, title, text, IconDialogError)
}

func NotifySend(urgency, title, text, icon string) {
	sessions := getDisplaySessions()
	if len(sessions) == 0 {
		pendingMu.Lock()
		queueNotification(pendingNotification{
			urgency: urgency,
			title:   title,
			text:    text,
			icon:    icon,
		})
		startNotificationWorker()
		pendingMu.Unlock()
		return
	}

	for _, session := range sessions {
		sendToSession(session, urgency, title, text, icon)
	}
}

var getDisplaySessions = func() []displaySession {
	var sessions []displaySession

	// If DISPLAY is set in environment, use it first
	if display, exists := os.LookupEnv("DISPLAY"); exists && display != "" {
		output, err := runNotificationCommandOutput("who")
		if err == nil {
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				if strings.Contains(line, display) {
					fields := strings.Fields(line)
					if len(fields) > 0 {
						sessions = append(sessions, displaySession{
							user:    strings.TrimSpace(fields[0]),
							display: display,
						})
						return sessions
					}
				}
			}
		}
	}

	// Fallback/Systemd: scan who for any active display session
	output, err := runNotificationCommandOutput("who")
	if err != nil {
		return nil
	}
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		lastField := fields[len(fields)-1]
		if strings.HasPrefix(lastField, "(:") && strings.HasSuffix(lastField, ")") {
			display := strings.Trim(lastField, "()")
			sessions = append(sessions, displaySession{
				user:    fields[0],
				display: display,
			})
		} else if strings.HasPrefix(lastField, ":") {
			sessions = append(sessions, displaySession{
				user:    fields[0],
				display: lastField,
			})
		}
	}

	return sessions
}

// notificationCmdTimeout bounds each command run to send a notification (who, systemd-run, notify-send),
// so a hanging D-Bus or systemd can't block the caller, e.g. a fan controller that stops after an error.
const notificationCmdTimeout = 5 * time.Second

// notificationCmdWaitDelay is how long a command run to send a notification may keep its output open
// after it exited or was killed (e.g. through a process it started).
const notificationCmdWaitDelay = 500 * time.Millisecond

// notificationCommand returns a command that is killed after the given timeout.
// Call cancel once the command has finished.
func notificationCommand(timeout time.Duration, name string, args ...string) (cmd *exec.Cmd, cancel context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	cmd = exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = notificationCmdWaitDelay
	return cmd, cancel
}

// runNotificationCommandOutput runs the given command with notificationCmdTimeout and returns its stdout.
func runNotificationCommandOutput(name string, args ...string) ([]byte, error) {
	cmd, cancel := notificationCommand(notificationCmdTimeout, name, args...)
	defer cancel()
	return cmd.Output()
}

var sendToSession = func(session displaySession, urgency, title, text, icon string) {
	u, err := user.Lookup(session.user)
	if err != nil {
		Error("Cannot lookup user %s: %v", session.user, err)
		return
	}

	dbusPath := "/run/user/" + u.Uid + "/bus"

	var notifCmd *exec.Cmd
	var cancel context.CancelFunc
	if os.Getuid() == 0 {
		notifCmd, cancel = notificationCommand(notificationCmdTimeout, "systemd-run",
			"--user",
			"--machine="+session.user+"@.host",
			"--setenv=DISPLAY="+session.display,
			"--setenv=DBUS_SESSION_BUS_ADDRESS=unix:path="+dbusPath,
			"notify-send",
			"-a", "fan2go",
			"-u", urgency,
			"-i", icon,
			title, text,
		)
	} else {
		notifCmd, cancel = notificationCommand(notificationCmdTimeout, "notify-send",
			"-a", "fan2go",
			"-u", urgency,
			"-i", icon,
			title, text,
		)
		notifCmd.Env = append(os.Environ(),
			"DISPLAY="+session.display,
			"DBUS_SESSION_BUS_ADDRESS=unix:path="+dbusPath,
		)
	}
	defer cancel()

	var stderr bytes.Buffer
	notifCmd.Stderr = &stderr

	err = notifCmd.Run()
	if err != nil {
		Error("Error sending notification to user %s on display %s: %v (Stderr: %s)", session.user, session.display, err, strings.TrimSpace(stderr.String()))
	}
}

// queueNotification adds notif to the pending notifications, dropping the oldest ones beyond
// maxPendingNotifications. pendingMu must be held.
func queueNotification(notif pendingNotification) {
	pendingNotifications = append(pendingNotifications, notif)
	if overflow := len(pendingNotifications) - maxPendingNotifications; overflow > 0 {
		pendingNotifications = append([]pendingNotification(nil), pendingNotifications[overflow:]...)
	}
}

// startNotificationWorker starts the worker that sends the pending notifications once a graphical session exists,
// unless it runs already. pendingMu must be held.
func startNotificationWorker() {
	if workerStarted {
		return
	}
	workerStarted = true
	go runNotificationWorker()
}

// runNotificationWorker waits for a graphical session and sends the pending notifications to it.
// It returns once no notification is pending anymore.
func runNotificationWorker() {
	for {
		<-workerWait()

		pendingMu.Lock()
		if len(pendingNotifications) == 0 {
			workerStarted = false
			pendingMu.Unlock()
			return
		}
		pendingMu.Unlock()

		sessions := getDisplaySessions()
		if len(sessions) == 0 {
			continue
		}

		pendingMu.Lock()
		queued := pendingNotifications
		pendingNotifications = nil
		pendingMu.Unlock()

		for _, session := range sessions {
			for _, notif := range queued {
				sendToSession(session, notif.urgency, notif.title, notif.text, notif.icon)
			}
		}

		pendingMu.Lock()
		if len(pendingNotifications) == 0 {
			// stop while holding the lock, so a notification queued from now on starts a new worker
			workerStarted = false
			pendingMu.Unlock()
			return
		}
		// notifications were queued while sending, send them on the next round
		pendingMu.Unlock()
	}
}
