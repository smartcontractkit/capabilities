package format

import "encoding/json"

// MultipleErrs deduplicates a slice of strings and includes the count of each unique error.
func MultipleErrs(errors []string) string {
	errorCounts := make(map[string]int)
	for _, err := range errors {
		errorCounts[err]++
	}

	b, err := json.Marshal(errorCounts)
	if err != nil {
		return "could not marshal errors"
	}
	return string(b)
}
