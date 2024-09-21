package util

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestContainsString_Valid(t *testing.T) {
	// GIVEN
	list := []string{
		"one",
		"two",
		"three",
	}

	// WHEN
	result := ContainsString(list, "two")

	// THEN
	assert.True(t, result)
}

func TestContainsString_Invalid(t *testing.T) {
	// GIVEN
	list := []string{
		"one",
		"two",
		"three",
	}

	// WHEN
	result := ContainsString(list, "zero")

	// THEN
	assert.False(t, result)
}

func TestSortSlice(t *testing.T) {
	// GIVEN
	list := []float64{
		3.0,
		1.0,
		2.0,
	}

	// WHEN
	sortSlice(list)

	// THEN
	assert.Equal(t, 1.0, list[0])
	assert.Equal(t, 2.0, list[1])
	assert.Equal(t, 3.0, list[2])
}

func TestSortedKeys(t *testing.T) {
	// GIVEN
	m := map[int]string{
		3: "three",
		1: "one",
		2: "two",
	}

	// WHEN
	result := SortedKeys(m)

	// THEN
	assert.Equal(t, 1, result[0])
	assert.Equal(t, 2, result[1])
	assert.Equal(t, 3, result[2])
}
