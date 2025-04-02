package ui

import (
	"fmt"
	"github.com/pterm/pterm"
	"os"
	"sync"
)

var (
	logMu = sync.Mutex{}
)

func SetDebugEnabled(enabled bool) {
	pterm.PrintDebugMessages = enabled
}

func Print(format string) {
	logMu.Lock()
	defer logMu.Unlock()
	pterm.Print(format)
}

func Printf(format string, a ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	pterm.Printf(format, a...)
}

func Println(format string) {
	logMu.Lock()
	defer logMu.Unlock()
	pterm.Println(format)
}

func Printfln(format string, a ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	pterm.Printfln(format, a...)
}

func Debug(format string, a ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	pterm.Debug.Printfln(format, a...)
}

func Success(format string, a ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	pterm.Success.Printfln(format, a...)
}

func Info(format string, a ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	pterm.Info.Printfln(format, a...)
}

func Warning(format string, a ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	pterm.Warning.Printfln(format, a...)
}

func WarningAndNotify(title string, format string, a ...interface{}) {
	Error(format, a...)
	NotifyError(title, fmt.Sprintf(format, a...))
}

func Error(format string, a ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	pterm.Error.Printfln(format, a...)
}

func ErrorAndNotify(title string, format string, a ...interface{}) {
	Error(format, a...)
	NotifyError(title, fmt.Sprintf(format, a...))
}

func FatalWithoutStacktrace(format string, a ...interface{}) {
	NotifyError("Fatal Error", fmt.Sprintf(format, a...))
	pterm.Fatal.WithFatal(false).Printfln(format, a...)
	os.Exit(1)
}

func Fatal(format string, a ...interface{}) {
	NotifyError("Fatal Error", fmt.Sprintf(format, a...))
	pterm.Fatal.Printfln(format, a...)
}
