package oracle

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

// TestObservationSizeIsAdditive verifies that adding a request observation to the observations map
// increases the total size by exactly the size of the individual request observation.
func TestObservationSizeIsAdditive(t *testing.T) {
	t.Parallel()
	chainHeight := types.ChainHeight{
		Finalized: 16,
		Safe:      32,
		Latest:    42,
	}
	ob := &types.Observation{ChainHeight: &chainHeight, Observations: make(map[string]*types.RequestObservation)}
	calculatedWireSize := proto.Size(ob)
	for i := range OCRRoundMaxBatchSize {
		id := fmt.Sprintf("request_%d", i)
		payloadSize := rand.Intn(12000)
		requestOb := &types.RequestObservation{
			Observation: &types.RequestObservation_EventuallyConsistent{EventuallyConsistent: make([]byte, payloadSize)},
		}
		calculatedWireSize += sizeOfNewMapElement(2, id, requestOb)
		ob.Observations[id] = requestOb
		rawObservation, err := proto.Marshal(ob)
		require.NoError(t, err)
		require.Equal(t, calculatedWireSize, len(rawObservation), "calculated wire size should match the actual size after adding request observation")
	}
}
