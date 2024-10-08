package jobs

import (
	"fmt"
	"regexp"
	"strings"
)

func ParseJobID(input string) (string, error) {
	lines := strings.Split(input, "\n")
	var idColumnIndex = -1

	// Regular expression to match rows containing data (with at least one ID)
	dataRowRe := regexp.MustCompile(`║\s*\d+\s*║`)

	// Find the index of the "ID" column
	for _, line := range lines {
		if strings.Contains(line, "ID") {
			// Split the header line by "║" and find the "ID" column index
			columns := strings.Split(line, "║")
			for i, column := range columns {
				if strings.Contains(strings.TrimSpace(column), "ID") {
					idColumnIndex = i
					break
				}
			}
		}
	}

	// Ensure the ID column was found
	if idColumnIndex == -1 {
		return "", fmt.Errorf("ID column not found")
	}

	// Extract IDs from subsequent lines
	for _, line := range lines {
		if dataRowRe.MatchString(line) {
			columns := strings.Split(line, "║")
			if len(columns) > idColumnIndex {
				return strings.TrimSpace(columns[idColumnIndex]), nil
			}
		}
	}

	return "", fmt.Errorf("ID not found")
}
