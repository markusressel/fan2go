package util

import (
	"golang.org/x/exp/constraints"
	"sort"
)

func ContainsString(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func Min(s []float64) float64 {
	if len(s) < 1 {
		return 0
	}
	if len(s) < 2 {
		return s[0]
	}
	result := s[0]
	for _, v := range s {
		if v < result {
			result = v
		}
	}
	return result
}

func Max(s []float64) float64 {
	if len(s) < 1 {
		return 0
	}
	if len(s) < 2 {
		return s[0]
	}
	result := s[0]
	for _, v := range s {
		if v > result {
			result = v
		}
	}
	return result
}

func sortSlice[T constraints.Ordered](s []T) {
	sort.Slice(s, func(i, j int) bool {
		return s[i] < s[j]
	})
}

func SortedKeys[T constraints.Ordered, K any](input map[T]K) []T {
	result := make([]T, 0, len(input))
	for k, _ := range input {
		result = append(result, k)
	}
	sortSlice(result)
	return result
}
