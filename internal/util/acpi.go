package util

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const acpiCallPath = "/proc/acpi/call"

// ExecuteAcpiCall writes method+args to /proc/acpi/call and returns the parsed integer result.
func ExecuteAcpiCall(method, args string) (int64, error) {
	return executeAcpiCallAt(acpiCallPath, acpiCallPath, method, args)
}

// executeAcpiCallAt writes the call to writePath and reads the result from readPath.
// In production both paths are the same (/proc/acpi/call). They are split for testing.
func executeAcpiCallAt(writePath, readPath, method, args string) (int64, error) {
	call := method
	if args != "" {
		call = method + " " + args
	}

	if err := os.WriteFile(writePath, []byte(call), 0); err != nil {
		return 0, fmt.Errorf("acpi_call: write failed: %w", err)
	}

	data, err := os.ReadFile(readPath)
	if err != nil {
		return 0, fmt.Errorf("acpi_call: read failed: %w", err)
	}

	result := strings.TrimRight(strings.TrimSpace(string(data)), "\x00")
	result = strings.TrimSpace(result)

	if strings.HasPrefix(strings.ToLower(result), "0x") {
		val, err := strconv.ParseInt(result[2:], 16, 64)
		if err != nil {
			return 0, fmt.Errorf("acpi_call: parse hex %q: %w", result, err)
		}
		return val, nil
	}

	val, err := strconv.ParseInt(result, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("acpi_call: parse %q: %w", result, err)
	}
	return val, nil
}
