package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAcpiPaths creates a write sink and a pre-populated read file for testing.
// writePath is a dummy sink; readPath contains the fake ACPI response.
func fakeAcpiPaths(t *testing.T, response string) (writePath, readPath string) {
	t.Helper()
	tmp := t.TempDir()
	writePath = filepath.Join(tmp, "write")
	readPath = filepath.Join(tmp, "read")
	require.NoError(t, os.WriteFile(writePath, []byte(""), 0o644))
	require.NoError(t, os.WriteFile(readPath, []byte(response), 0o644))
	return writePath, readPath
}

func TestExecuteAcpiCallAt_HexResult(t *testing.T) {
	w, r := fakeAcpiPaths(t, "0x2A\x00")

	val, err := executeAcpiCallAt(w, r, `\_SB.METH`, "")
	require.NoError(t, err)
	assert.Equal(t, int64(42), val)
}

func TestExecuteAcpiCallAt_HexUppercase(t *testing.T) {
	w, r := fakeAcpiPaths(t, "0xFF\x00")

	val, err := executeAcpiCallAt(w, r, `\_SB.METH`, "")
	require.NoError(t, err)
	assert.Equal(t, int64(255), val)
}

func TestExecuteAcpiCallAt_DecimalResult(t *testing.T) {
	w, r := fakeAcpiPaths(t, "38000\n")

	val, err := executeAcpiCallAt(w, r, `\_SB.METH`, "")
	require.NoError(t, err)
	assert.Equal(t, int64(38000), val)
}

func TestExecuteAcpiCallAt_WithArgs(t *testing.T) {
	w, r := fakeAcpiPaths(t, "100\n")

	val, err := executeAcpiCallAt(w, r, `\_SB.METH`, "0 0x13")
	require.NoError(t, err)
	assert.Equal(t, int64(100), val)
}

func TestExecuteAcpiCallAt_MissingFile(t *testing.T) {
	_, err := executeAcpiCallAt("/nonexistent/path/call", "/nonexistent/path/call", `\_SB.METH`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write failed")
}

func TestExecuteAcpiCallAt_MalformedOutput(t *testing.T) {
	w, r := fakeAcpiPaths(t, "not_a_number")

	_, err := executeAcpiCallAt(w, r, `\_SB.METH`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestExecuteAcpiCallAt_MalformedHex(t *testing.T) {
	w, r := fakeAcpiPaths(t, "0xGGGG")

	_, err := executeAcpiCallAt(w, r, `\_SB.METH`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse hex")
}

func TestExecuteAcpiCallAt_NullPaddedHex(t *testing.T) {
	w, r := fakeAcpiPaths(t, "0x1E\x00\x00\x00")

	val, err := executeAcpiCallAt(w, r, `\_SB.METH`, "")
	require.NoError(t, err)
	assert.Equal(t, int64(30), val)
}
