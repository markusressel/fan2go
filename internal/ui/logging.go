package ui

import (
	"github.com/pterm/pterm"
)

func Printf(format string, a ...interface{}) {
	pterm.Printf(format, a...)
}

func Println(format string, a ...interface{}) {
	pterm.Printfln(format, a...)
}

func Debug(format string, a ...interface{}) {
	// TODO: set this based on --verbose flag
	pterm.PrintDebugMessages = true
	pterm.Debug.Printfln(format, a...)
}

func Info(format string, a ...interface{}) {
	pterm.Info.Printfln(format, a...)
}

func Warning(format string, a ...interface{}) {
	pterm.Warning.Printfln(format, a...)
}

func Error(format string, a ...interface{}) {
	pterm.Error.Printfln(format, a...)
}

func Fatal(format string, a ...interface{}) {
	pterm.Fatal.Printfln(format, a...)
}
