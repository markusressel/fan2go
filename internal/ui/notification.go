package ui

import (
	"os"
	"os/exec"
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

var (
	pendingMu            sync.Mutex
	pendingNotifications []pendingNotification
	workerStarted        bool
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
		pendingNotifications = append(pendingNotifications, pendingNotification{
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

func getDisplaySessions() []displaySession {
	var sessions []displaySession

	// If DISPLAY is set in environment, use it first
	if display, exists := os.LookupEnv("DISPLAY"); exists && display != "" {
		cmd := exec.Command("who")
		output, err := cmd.Output()
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
	cmd := exec.Command("who")
	output, err := cmd.Output()
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

func sendToSession(session displaySession, urgency, title, text, icon string) {
	cmd := exec.Command("id", "-u", session.user)
	output, err := cmd.Output()
	if err != nil {
		Error("Cannot send notification, unable to detect user id: %v", err)
		return
	}
	userIdString := strings.TrimSpace(string(output))
	if len(userIdString) <= 0 {
		Error("Cannot send notification, user id empty")
		return
	}

	dbusPath := "/run/user/" + userIdString + "/bus"

	var notifCmd *exec.Cmd
	if os.Getuid() == 0 {
		notifCmd = exec.Command("sudo", "-u", session.user,
			"DISPLAY="+session.display,
			"DBUS_SESSION_BUS_ADDRESS=unix:path="+dbusPath,
			"notify-send",
			"-a", "fan2go",
			"-u", urgency,
			"-i", icon,
			title, text,
		)
	} else {
		notifCmd = exec.Command("notify-send",
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

	err = notifCmd.Run()
	if err != nil {
		Error("Error sending notification to user %s on display %s: %v", session.user, session.display, err)
	}
}

func startNotificationWorker() {
	if workerStarted {
		return
	}
	workerStarted = true
	go func() {
		for {
			time.Sleep(15 * time.Second)

			pendingMu.Lock()
			if len(pendingNotifications) == 0 {
				workerStarted = false
				pendingMu.Unlock()
				return
			}

			sessions := getDisplaySessions()
			if len(sessions) > 0 {
				for _, session := range sessions {
					for _, notif := range pendingNotifications {
						sendToSession(session, notif.urgency, notif.title, notif.text, notif.icon)
					}
				}
				pendingNotifications = nil
				workerStarted = false
				pendingMu.Unlock()
				return
			}
			pendingMu.Unlock()
		}
	}()
}
