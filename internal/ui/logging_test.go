package ui

import (
	"github.com/pterm/pterm"
	"os"
)

func ExamplePrintln() {
	pterm.SetDefaultOutput(os.Stdout)
	pterm.DisableStyling()

	msg := "This is a test %d"
	a := 5
	Printfln(msg, a)
	// Output:
	// This is a test 5
}

func ExampleDebug() {
	pterm.SetDefaultOutput(os.Stdout)
	pterm.DisableStyling()
	pterm.PrintDebugMessages = true

	msg := "This is a test: %d"
	a := 5
	Debug(msg, a)
	// Output:
	// DEBUG: This is a test: 5
}

func ExampleInfo() {
	pterm.SetDefaultOutput(os.Stdout)
	pterm.DisableStyling()

	msg := "This is a test: %d"
	a := 5
	Info(msg, a)
	// Output:
	// INFO: This is a test: 5
}

func ExampleWarning() {
	pterm.SetDefaultOutput(os.Stdout)
	pterm.DisableStyling()

	msg := "This is a test: %d"
	a := 5
	Warning(msg, a)
	// Output:
	// WARNING: This is a test: 5
}

func ExampleError() {
	pterm.SetDefaultOutput(os.Stdout)
	pterm.DisableStyling()

	msg := "This is a test: %v"
	a := os.ErrClosed
	Error(msg, a)
	// Output:
	// ERROR: This is a test: file already closed
}
