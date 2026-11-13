//go:build !cgo

package lmsensors

import "fmt"

// Load always fails in a build without cgo: libsensors can't be loaded.
func Load() (string, error) {
	return "", fmt.Errorf("%w: %w", ErrUnavailable, ErrNoCgo)
}

// DetectedChips always fails in a build without cgo, see Load.
func DetectedChips(_ WantValue) ([]Chip, error) {
	_, err := Load()
	return nil, err
}

// Version always returns "" in a build without cgo, see Load.
func Version() string {
	return ""
}

// ConfigUsed always returns false in a build without cgo, see Load.
func ConfigUsed() bool {
	return false
}
