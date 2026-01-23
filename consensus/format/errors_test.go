package format

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestErrorsForLogging(t *testing.T) {
	t.Run("empty errors list", func(t *testing.T) {
		result := MultipleErrs([]string{})

		var decoded []map[string]interface{}
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Empty(t, decoded)
	})

	t.Run("single unique error", func(t *testing.T) {
		errors := []string{"error 1"}
		result := MultipleErrs(errors)

		var decoded []map[string]interface{}
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Len(t, decoded, 1)
		require.Equal(t, "error 1", decoded[0]["error"])
		require.Equal(t, float64(1), decoded[0]["count"]) // JSON numbers are float64
	})

	t.Run("multiple unique errors", func(t *testing.T) {
		errors := []string{"error 1", "error 2", "error 3"}
		result := MultipleErrs(errors)

		var decoded []map[string]interface{}
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Len(t, decoded, 3)

		// Check that all errors are present with count 1
		errorMap := make(map[string]float64)
		for _, item := range decoded {
			errorMap[item["error"].(string)] = item["count"].(float64)
		}
		require.Equal(t, float64(1), errorMap["error 1"])
		require.Equal(t, float64(1), errorMap["error 2"])
		require.Equal(t, float64(1), errorMap["error 3"])
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

		var decoded []map[string]interface{}
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Len(t, decoded, 2) // Two unique errors

		// Build map for easier checking
		errorMap := make(map[string]float64)
		for _, item := range decoded {
			errorMap[item["error"].(string)] = item["count"].(float64)
		}

		require.Equal(t, float64(3), errorMap["request size exceeds limit"])
		require.Equal(t, float64(2), errorMap["invalid input"])
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

		var decoded []map[string]interface{}
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Len(t, decoded, 1)
		require.Equal(t, "same error", decoded[0]["error"])
		require.Equal(t, float64(5), decoded[0]["count"])
	})

	t.Run("error with special characters", func(t *testing.T) {
		errors := []string{
			"error with \"quotes\" and\nnewlines",
			"error with \"quotes\" and\nnewlines",
		}
		result := MultipleErrs(errors)

		var decoded []map[string]interface{}
		err := json.Unmarshal([]byte(result), &decoded)
		require.NoError(t, err)
		require.Len(t, decoded, 1)
		require.Equal(t, "error with \"quotes\" and\nnewlines", decoded[0]["error"])
		require.Equal(t, float64(2), decoded[0]["count"])
	})
}
