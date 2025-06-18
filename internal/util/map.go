package util

import "golang.org/x/exp/constraints"

// ExtractKeysWithDistinctValues extracts the keys from a map with distinct values.
// It returns a slice of keys where each key corresponds to a unique value in the map.
func ExtractKeysWithDistinctValues(input map[int]int) []int {
	var result []int
	seenValues := make(map[int]bool)

	var keys = SortedKeys(input)

	for _, key := range keys {
		value := input[key]
		if !seenValues[value] {
			seenValues[value] = true
			result = append(result, key)
		}
	}
	return result
}

// ExtractIndicesWithDistinctValues extracts the indices of elements with distinc values from a slice.
// It returns a slice of indices where each index corresponds to a unique value in the slice
func ExtractIndicesWithDistinctValues(input []int) []int {
	var result []int
	seenValues := make(map[int]bool)

	for index, value := range input {
		if !seenValues[value] {
			seenValues[value] = true
			result = append(result, index)
		}
	}
	return result
}

func Values[A constraints.Ordered, B any](input map[A]B) []B {
	var result []B
	for _, b := range input {
		result = append(result, b)
	}
	return result
}
