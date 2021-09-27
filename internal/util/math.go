package util

// Avg calculates the average of all values in the given array
func Avg(values []float64) float64 {
	// declaring a variable
	// to store the sum
	sum := 0.0
	// traversing through the
	// array using for loop
	for i := 0; i < len(values); i++ {
		// adding the values of
		// array to the variable sum
		sum += values[i]
	}
	// declaring a variable
	// avg to find the average
	return sum / (float64(len(values)))
}
