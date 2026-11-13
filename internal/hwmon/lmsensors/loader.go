package lmsensors

import (
	"fmt"
	"strings"
)

// LibraryNames are the names libsensors is loaded by, in this order: the current soname, the soname Fedora, RHEL and
// openSUSE still use for the same library, and the unversioned development symlink.
var LibraryNames = []string{"libsensors.so.5", "libsensors.so.4", "libsensors.so"}

// openFunc loads the library with the given name
type openFunc func(name string) error

// loadFirst loads the first library of names that opens successfully and returns its name.
// If none does, the error lists the error of every name.
func loadFirst(names []string, open openFunc) (string, error) {
	var errs []string
	for _, name := range names {
		err := open(name)
		if err == nil {
			return name, nil
		}
		// the errors of dlopen() start with the library name already
		errs = append(errs, err.Error())
	}
	return "", fmt.Errorf("%w (%s)", ErrUnavailable, strings.Join(errs, "; "))
}
