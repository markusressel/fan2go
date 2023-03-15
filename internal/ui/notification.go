package ui

import (
	"os"
	"os/exec"
	"strings"
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
	display, exists := os.LookupEnv("DISPLAY")
	if !exists {
		Warning("Cannot send notification, missing env variable 'DISPLAY'!")
		return
	}

	cmd := exec.Command("who")
	output, err := cmd.Output()
	if err != nil {
		Warning("Cannot send notification, unable to find user of display session: %v", err)
		return
	}
	lines := strings.Split(string(output), "\n")
	var user string
	for _, line := range lines {
		if strings.Contains(line, display) {
			user = strings.TrimSpace(strings.Fields(line)[0])
			break
		}
	}

	if len(user) <= 0 {
		Warning("Cannot send notification, unable to detect user of current display session")
		return
	}

	cmd = exec.Command("id", "-u", user)
	output, err = cmd.Output()
	userIdString := strings.TrimSpace(string(output))
	if len(userIdString) <= 0 {
		Warning("Cannot send notification, unable to detect user id: %s", err.Error())
		return
	}

	cmd = exec.Command("sudo", "-u", user,
		"DISPLAY="+display,
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/"+userIdString+"/bus",
		"notify-send",
		"-a", "fan2go",
		"-u", urgency,
		"-i", icon,
		title, text,
	)
	err = cmd.Run()
	if err != nil {
		Error("Error sending notification: %v", err)
	}
}
