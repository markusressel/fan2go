package ui

import (
	"os"
)

func ExamplePrintln() {
	msg := "This is a test "
	a := 5
	Printfln(msg, a)
	// Output:
	// This is a test 5
}

func ExampleDebug() {
	msg := "This is a test: %d"
	a := 5
	Debug(msg, a)
	// Output:
	// DEBUG: This is a test: 5
}

func ExampleInfo() {
	msg := "This is a test: %d"
	a := 5
	Info(msg, a)
	// Output:
	// INFO: This is a test: 5
}

func ExampleWarning() {
	msg := "This is a test: %d"
	a := 5
	Warning(msg, a)
	// Output:
	// WARNING: This is a test: 5
}

func ExampleError() {
	msg := "This is a test: %v"
	a := os.ErrClosed
	Error(msg, a)
	// Output:
	// ERROR: This is a test: file already closed
}
