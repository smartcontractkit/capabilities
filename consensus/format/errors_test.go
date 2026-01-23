package format

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorsForLogging(t *testing.T) {
	t.Run("empty errors list", func(t *testing.T) {
		result := MultipleErrs([]string{})

		// Validate the marshalled string
		require.Equal(t, "{}", result)

		// Validate it can be unmarshalled
		var decoded map[string]int
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Empty(t, decoded)
	})

	t.Run("single unique error", func(t *testing.T) {
		errors := []string{"error 1"}
		result := MultipleErrs(errors)

		// Validate the marshalled string is valid JSON and contains expected content
		require.True(t, json.Valid([]byte(result)), "result should be valid JSON")
		require.Contains(t, result, `"error 1"`)
		require.Contains(t, result, `:1`)

		// Validate it can be unmarshalled correctly
		var decoded map[string]int
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Len(t, decoded, 1)
		require.Equal(t, 1, decoded["error 1"])
	})

	t.Run("multiple unique errors", func(t *testing.T) {
		errors := []string{"error 1", "error 2", "error 3"}
		result := MultipleErrs(errors)

		// Validate the marshalled string is valid JSON and contains all errors
		require.True(t, json.Valid([]byte(result)), "result should be valid JSON")
		require.Contains(t, result, `"error 1"`)
		require.Contains(t, result, `"error 2"`)
		require.Contains(t, result, `"error 3"`)

		// Validate it can be unmarshalled correctly
		var decoded map[string]int
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Len(t, decoded, 3)
		require.Equal(t, 1, decoded["error 1"])
		require.Equal(t, 1, decoded["error 2"])
		require.Equal(t, 1, decoded["error 3"])
	})

	t.Run("duplicate errors with counts", func(t *testing.T) {
		errors := []string{
			"request size exceeds limit",
			"request size exceeds limit",
			"request size exceeds limit",
			"invalid input",
			"invalid input",
		}
		result := MultipleErrs(errors)

		// Validate the marshalled string is valid JSON and contains both errors with correct counts
		require.True(t, json.Valid([]byte(result)), "result should be valid JSON")
		require.Contains(t, result, `"request size exceeds limit"`)
		require.Contains(t, result, `"invalid input"`)
		require.Regexp(t, `"request size exceeds limit"\s*:\s*3`, result)
		require.Regexp(t, `"invalid input"\s*:\s*2`, result)

		// Validate it can be unmarshalled correctly
		var decoded map[string]int
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Len(t, decoded, 2) // Two unique errors
		require.Equal(t, 3, decoded["request size exceeds limit"])
		require.Equal(t, 2, decoded["invalid input"])
	})

	t.Run("all same error", func(t *testing.T) {
		errors := []string{
			"same error",
			"same error",
			"same error",
			"same error",
			"same error",
		}
		result := MultipleErrs(errors)

		// Validate the marshalled string is valid JSON and contains the error and count
		require.True(t, json.Valid([]byte(result)), "result should be valid JSON")
		require.Contains(t, result, `"same error"`)
		require.Regexp(t, `"same error"\s*:\s*5`, result)

		// Validate it can be unmarshalled correctly
		var decoded map[string]int
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Len(t, decoded, 1)
		require.Equal(t, 5, decoded["same error"])
	})

	t.Run("error with special characters", func(t *testing.T) {
		errors := []string{
			"error with \"quotes\" and\nnewlines",
			"error with \"quotes\" and\nnewlines",
		}
		result := MultipleErrs(errors)

		// Validate the marshalled string is valid JSON and contains the error (JSON escaped) and count
		require.True(t, json.Valid([]byte(result)), "result should be valid JSON")
		require.Contains(t, result, `error with`)
		require.Contains(t, result, `quotes`)
		require.Contains(t, result, `:2`)

		// Validate it can be unmarshalled correctly
		var decoded map[string]int
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Len(t, decoded, 1)
		require.Equal(t, 2, decoded["error with \"quotes\" and\nnewlines"])
	})
}
