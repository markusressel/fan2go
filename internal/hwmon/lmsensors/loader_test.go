package lmsensors

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadFirst_TriesNamesInOrder(t *testing.T) {
	var tried []string
	available := map[string]bool{"libsensors.so.4": true, "libsensors.so": true}
	open := func(name string) error {
		tried = append(tried, name)
		if available[name] {
			return nil
		}
		return errors.New("not found")
	}

	name, err := loadFirst(LibraryNames, open)

	assert.NoError(t, err)
	assert.Equal(t, "libsensors.so.4", name)
	assert.Equal(t, []string{"libsensors.so.5", "libsensors.so.4"}, tried)
}

func TestLoadFirst_PrefersCurrentSoname(t *testing.T) {
	var tried []string
	open := func(name string) error {
		tried = append(tried, name)
		return nil
	}

	name, err := loadFirst(LibraryNames, open)

	assert.NoError(t, err)
	assert.Equal(t, "libsensors.so.5", name)
	assert.Equal(t, []string{"libsensors.so.5"}, tried)
}

func TestLoadFirst_FallsBackToUnversionedName(t *testing.T) {
	open := func(name string) error {
		if name == "libsensors.so" {
			return nil
		}
		return errors.New("not found")
	}

	name, err := loadFirst(LibraryNames, open)

	assert.NoError(t, err)
	assert.Equal(t, "libsensors.so", name)
}

func TestLoadFirst_NoneAvailable(t *testing.T) {
	var tried []string
	open := func(name string) error {
		tried = append(tried, name)
		return errors.New("cannot open " + name)
	}

	name, err := loadFirst(LibraryNames, open)

	assert.Equal(t, "", name)
	assert.ErrorIs(t, err, ErrUnavailable)
	assert.Equal(t, LibraryNames, tried)
	for _, n := range LibraryNames {
		assert.Contains(t, err.Error(), "cannot open "+n)
	}
}
