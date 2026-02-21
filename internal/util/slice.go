package util

import (
	"cmp"
	"sort"
)

// ContainsString checks if the slice of strings contains the specified string.
func ContainsString(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

// sortSlice sorts the input slice in ascending order.
func sortSlice[T cmp.Ordered](s []T) {
	sort.Slice(s, func(i, j int) bool {
		return s[i] < s[j]
	})
}

// SortedKeys returns the keys of the input map sorted in ascending order.
func SortedKeys[T cmp.Ordered, K any](input map[T]K) []T {
	result := make([]T, 0, len(input))
	for k := range input {
		result = append(result, k)
	}
	sortSlice(result)
	return result
}
