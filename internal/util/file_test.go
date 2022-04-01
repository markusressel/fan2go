package util

import (
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

// TODO: these tests need to run as root

func TestFileHasPermissionsUserIsRoot(t *testing.T) {
	// GIVEN
	filePath := "./testfile"

	filePerm := os.FileMode(0o700)
	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
	assert.NoError(t, err)
	err = os.Chown(filePath, 0, 1000)
	assert.NoError(t, err)
	err = os.Chmod(filePath, filePerm)
	assert.NoError(t, err)

	defer file.Close()
	defer os.Remove(filePath)

	// WHEN
	result, err := CheckFilePermissionsForExecution(filePath)

	// THEN
	assert.Equal(t, true, result)
	assert.NoError(t, err)
}

func TestFileHasPermissionsGroupIsRootAndHasWrite(t *testing.T) {
	// GIVEN
	filePath := "./testfile"

	filePerm := os.FileMode(0o770)
	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
	assert.NoError(t, err)
	err = os.Chown(filePath, 0, 0)
	assert.NoError(t, err)
	err = os.Chmod(filePath, filePerm)
	assert.NoError(t, err)

	defer file.Close()
	defer os.Remove(filePath)

	// WHEN
	result, err := CheckFilePermissionsForExecution(filePath)

	// THEN
	assert.Equal(t, true, result)
	assert.NoError(t, err)
}

func TestFileHasPermissionsGroupOtherThanRootHasWritePermission(t *testing.T) {
	// GIVEN
	filePath := "./testfile"

	filePerm := os.FileMode(0o720)
	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
	assert.NoError(t, err)
	err = os.Chown(filePath, 0, 1000)
	assert.NoError(t, err)
	err = os.Chmod(filePath, filePerm)
	assert.NoError(t, err)

	defer file.Close()
	defer os.Remove(filePath)

	// WHEN
	result, err := CheckFilePermissionsForExecution(filePath)

	// THEN
	assert.Equal(t, false, result)
	assert.Error(t, err)
}

func TestFileHasPermissionsOtherHasWritePermission(t *testing.T) {
	// GIVEN
	filePath := "./testfile"

	filePerm := os.FileMode(0o702)
	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
	assert.NoError(t, err)
	err = os.Chown(filePath, 0, 1000)
	assert.NoError(t, err)
	err = os.Chmod(filePath, filePerm)
	assert.NoError(t, err)

	defer file.Close()
	defer os.Remove(filePath)

	// WHEN
	result, err := CheckFilePermissionsForExecution(filePath)

	// THEN
	assert.Equal(t, false, result)
	assert.Error(t, err)
}
