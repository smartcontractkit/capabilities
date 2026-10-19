package service

import (
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/scheduler/cron/protos"
)

func TestNew(t *testing.T) {
	c := startCrons(t, clockwork.NewFakeClock(), Config{FastestScheduleIntervalSeconds: 1}, 1)[0]

	info, err := c.Info(t.Context())
	require.NoError(t, err)
	assert.Equal(t, protos.CronID, info.ID)
	assert.NoError(t, c.Ready())
}
