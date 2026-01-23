package format

import "encoding/json"

// MultipleErrs deduplicates a slice of strings and includes the count of each unique error.
func MultipleErrs(errors []string) string {
	errorCounts := make(map[string]int)
	for _, err := range errors {
		errorCounts[err]++
	}

	type errorWithCount struct {
		Error string `json:"error"`
		Count int    `json:"count"`
	}

	var errorsWithCounts []errorWithCount
	for err, count := range errorCounts {
		errorsWithCounts = append(errorsWithCounts, errorWithCount{
			Error: err,
			Count: count,
		})
	}

	b, err := json.Marshal(errorsWithCounts)
	if err != nil {
		return "could not marshal errors"
	}
	return string(b)
}
