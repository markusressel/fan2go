package ui

import (
	"github.com/mqu/go-notify"
)

// For a list of possible icons, see: https://specifications.freedesktop.org/icon-naming-spec/icon-naming-spec-latest.html
const (
	IconDialogError = "dialog-error"
	IconDialogInfo  = "dialog-information"
	IconDialogWarn  = "dialog-warning"
)

func init() {
	notify.Init("fan2go")
}

func NotifyInfo(title, text string) {
	NotifySend(title, text, IconDialogInfo)
}

func NotifyWarn(title, text string) {
	NotifySend(title, text, IconDialogWarn)
}

func NotifyError(title, text string) {
	NotifySend(title, text, IconDialogError)
}

func NotifySend(title, text, icon string) {
	hello := notify.NotificationNew(title, text, icon)
	err := hello.Show()
	if err != nil {
		Error("Error sending notification: %v", err)
	}
}
