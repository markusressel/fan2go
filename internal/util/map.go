package util

func ExtractKeysWithDistinctValues(input map[int]int) []int {
	var result []int

	var keys = SortedKeys(input)

	lastDistinctOutput := -1
	for _, key := range keys {
		value := input[key]
		if lastDistinctOutput == -1 || lastDistinctOutput != value {
			lastDistinctOutput = value
			result = append(result, key)
		}
	}
	return result
}
