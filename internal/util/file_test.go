package util

import (
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

func TestFileHasPermissionsUserIsRoot(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Skipping tests which require root")
	}

	// GIVEN
	filePath := "./testfile"

	filePerm := os.FileMode(0o700)
	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
	assert.NoError(t, err)
	err = os.Chown(filePath, 0, 1000)
	assert.NoError(t, err)
	err = os.Chmod(filePath, filePerm)
	assert.NoError(t, err)

	defer func(file *os.File) {
		_ = file.Close()
	}(file)
	defer func(name string) {
		_ = os.Remove(name)
	}(filePath)

	// WHEN
	result, err := CheckFilePermissionsForExecution(filePath)

	// THEN
	assert.Equal(t, true, result)
	assert.NoError(t, err)
}

func TestFileHasPermissionsGroupIsRootAndHasWrite(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Skipping tests which require root")
	}

	// GIVEN
	filePath := "./testfile"

	filePerm := os.FileMode(0o770)
	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
	assert.NoError(t, err)
	err = os.Chown(filePath, 0, 0)
	assert.NoError(t, err)
	err = os.Chmod(filePath, filePerm)
	assert.NoError(t, err)

	defer func(file *os.File) {
		_ = file.Close()
	}(file)
	defer func(name string) {
		_ = os.Remove(name)
	}(filePath)

	// WHEN
	result, err := CheckFilePermissionsForExecution(filePath)

	// THEN
	assert.Equal(t, true, result)
	assert.NoError(t, err)
}

func TestFileHasPermissionsGroupOtherThanRootHasWritePermission(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Skipping tests which require root")
	}

	// GIVEN
	filePath := "./testfile"

	filePerm := os.FileMode(0o720)
	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
	assert.NoError(t, err)
	err = os.Chown(filePath, 0, 1000)
	assert.NoError(t, err)
	err = os.Chmod(filePath, filePerm)
	assert.NoError(t, err)

	defer func(file *os.File) {
		_ = file.Close()
	}(file)
	defer func(name string) {
		_ = os.Remove(name)
	}(filePath)

	// WHEN
	result, err := CheckFilePermissionsForExecution(filePath)

	// THEN
	assert.Equal(t, false, result)
	assert.Error(t, err)
}

func TestFileHasPermissionsOtherHasWritePermission(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Skipping tests which require root")
	}

	// GIVEN
	filePath := "./testfile"

	filePerm := os.FileMode(0o702)
	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, filePerm)
	assert.NoError(t, err)
	err = os.Chown(filePath, 0, 1000)
	assert.NoError(t, err)
	err = os.Chmod(filePath, filePerm)
	assert.NoError(t, err)

	defer func(file *os.File) {
		_ = file.Close()
	}(file)
	defer func(name string) {
		_ = os.Remove(name)
	}(filePath)

	// WHEN
	result, err := CheckFilePermissionsForExecution(filePath)

	// THEN
	assert.Equal(t, false, result)
	assert.Error(t, err)
}

func TestReadIntFromFile_Success(t *testing.T) {
	// GIVEN
	filePath := "../../test/file_fan_rpm"

	// WHEN
	result, err := ReadIntFromFile(filePath)

	// THEN
	assert.Equal(t, 2150, result)
	assert.NoError(t, err)
}

func TestReadIntFromFile_FileNotFound(t *testing.T) {
	// GIVEN
	filePath := "../../not exists"

	// WHEN
	result, err := ReadIntFromFile(filePath)

	// THEN
	assert.Equal(t, -1, result)
	assert.Error(t, err)
}

func TestReadIntFromFile_FileEmpty(t *testing.T) {
	// GIVEN
	filePath := "./empty_file"
	_, _ = os.Create(filePath)
	defer func(name string) {
		_ = os.Remove(name)
	}(filePath)

	// WHEN
	result, err := ReadIntFromFile(filePath)

	// THEN
	assert.Equal(t, -1, result)
	assert.Error(t, err)
}

func TestWriteIntToFile_Success(t *testing.T) {
	// GIVEN
	filePath := "./testfile"
	defer func(name string) {
		_ = os.Remove(name)
	}(filePath)
	value := 123

	// WHEN
	err := WriteIntToFile(value, filePath)

	// THEN
	assert.NoError(t, err)

	// WHEN
	result, err := ReadIntFromFile(filePath)

	// THEN
	assert.Equal(t, value, result)
}

func TestWriteIntToFile_InvalidPath(t *testing.T) {
	// GIVEN
	filePath := ".////"
	value := 123

	// WHEN
	err := WriteIntToFile(value, filePath)

	// THEN
	assert.Error(t, err)
}
