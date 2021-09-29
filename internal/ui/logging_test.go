package ui

import (
	"os"
	"testing"
)

func TestPrintln(t *testing.T) {
	msg := "This is a test: %d"
	a := 5
	Println(msg, a)
	// Output:
	// This is a test: 5
}

func TestDebug(t *testing.T) {
	msg := "This is a test: %d"
	a := 5
	Debug(msg, a)
	// Output:
	// DEBUG: This is a test: 5
}

func TestInfo(t *testing.T) {
	msg := "This is a test: %d"
	a := 5
	Info(msg, a)
	// Output:
	// INFO: This is a test: 5
}

func TestWarning(t *testing.T) {
	msg := "This is a test: %d"
	a := 5
	Warning(msg, a)
	// Output:
	// WARNING: This is a test: 5
}

func TestError(t *testing.T) {
	msg := "This is a test: %v"
	a := os.ErrClosed
	Error(msg, a)
	// Output:
	// ERROR: This is a test: file already closed
}
