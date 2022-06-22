package util

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
