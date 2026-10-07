package trigger

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/custmsg"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/events"
)

func TestWithCapabilityDonID(t *testing.T) {
	t.Parallel()

	t.Run("labels events with the capability DON ID", func(t *testing.T) {
		t.Parallel()
		labels := withCapabilityDonID(custmsg.NewLabeler(), 2).Labels()
		require.Equal(t, "2", labels[events.KeyDonID])
	})

	t.Run("leaves DON ID unset when unknown", func(t *testing.T) {
		t.Parallel()
		labels := withCapabilityDonID(custmsg.NewLabeler(), 0).Labels()
		require.NotContains(t, labels, events.KeyDonID)
	})
}
