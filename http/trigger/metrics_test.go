package trigger

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewMetrics(t *testing.T) {
	metrics, err := NewMetrics()
	require.NoError(t, err, "NewMetrics should not return an error")
	require.NotNil(t, metrics, "NewMetrics should return a non-nil instance")
}
