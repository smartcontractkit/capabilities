package actions

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func unixMicroUint64(t *testing.T, tm time.Time) uint64 {
	t.Helper()

	micros := tm.UnixMicro()
	require.GreaterOrEqual(t, micros, int64(0))

	out, err := strconv.ParseUint(strconv.FormatInt(micros, 10), 10, 64)
	require.NoError(t, err)

	return out
}
