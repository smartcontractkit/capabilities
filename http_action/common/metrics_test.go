package common

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestNewMetrics(t *testing.T) {
	metrics, err := NewMetrics(noop.Meter{})
	require.NoError(t, err, "NewMetrics should not return an error")
	require.NotNil(t, metrics, "NewMetrics should return a non-nil instance")
}
