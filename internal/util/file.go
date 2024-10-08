package util

import (
	"errors"
	"fmt"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/natefinch/atomic"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// CheckFilePermissionsForExecution checks whether the given filePath owner, group and permissions
// are safe to use this file for execution by fan2go.
func CheckFilePermissionsForExecution(filePath string) (bool, error) {
	var file = filePath

	file, err := filepath.EvalSymlinks(file)
	if err != nil {
		return false, err
	}

	info, err := os.Stat(file)
	if os.IsNotExist(err) {
		return false, errors.New("file not found")
	}

	stat := info.Sys().(*syscall.Stat_t)
	if stat.Uid != 0 {
		return false, errors.New("owner is not root")
	}

	if stat.Gid != 0 {
		mode := info.Mode()
		groupWrite := mode & (os.FileMode(0o020))
		if groupWrite != 0 {
			return false, errors.New("group is not root but has write permission")
		}
	}

	otherWrite := info.Mode() & (os.FileMode(0o002))
	if otherWrite != 0 {
		return false, errors.New("others have write permission")
	}

	return true, nil
}

func ReadIntFromFile(path string) (value int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return -1, err
	}
	text := string(data)
	if len(text) <= 0 {
		return -1, fmt.Errorf("file is empty: %s", path)
	}
	text = strings.TrimSpace(text)
	value, err = strconv.Atoi(text)
	return value, err
}

// WriteIntToFile write a single integer to a file.go path
func WriteIntToFile(value int, path string) error {
	evaluatedPath, err := resolvePath(path)
	if len(evaluatedPath) > 0 && err == nil {
		path = evaluatedPath
	}
	valueAsString := fmt.Sprintf("%d", value)

	err = os.WriteFile(path, []byte(valueAsString), 0644)
	return err
}

func resolvePath(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func WriteIntToFileAtomic(value int, path string) error {
	evaluatedPath, err := resolvePath(path)
	if len(evaluatedPath) > 0 && err == nil {
		path = evaluatedPath
	}
	valueAsString := fmt.Sprintf("%d", value)
	valueReader := strings.NewReader(valueAsString)
	return atomic.WriteFile(evaluatedPath, valueReader)
}

// FindFilesMatching finds all files in a given directory, matching the given regex
func FindFilesMatching(path string, expr *regexp.Regexp) []string {
	var result []string
	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			ui.Fatal("File error: %v", err)
		}

		if !info.IsDir() && expr.MatchString(info.Name()) {
			var devicePath string

			// we may need to adjust the path (pwmconfig cite...)
			_, err := os.Stat(path + "/name")
			if os.IsNotExist(err) {
				devicePath = path + "/device"
			} else {
				devicePath = path
			}

			devicePath, err = filepath.EvalSymlinks(devicePath)
			if err != nil {
				panic(err)
			}

			result = append(result, devicePath)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}

	return result
}
