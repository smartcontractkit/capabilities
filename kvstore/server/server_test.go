package server

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/kvstore/oracle"
	"github.com/smartcontractkit/capabilities/libs/testutils"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/ocr3cap"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

func TestNewCapabilities(t *testing.T) {
	logger := testutils.NewLogger(t)
	capabilitiesServer := New(&loop.Server{
		Logger: logger,
	}, "kv-store-test-service")
	assert.NotNil(t, capabilitiesServer)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	assert.NoError(t, capabilitiesServer.Start(ctx))

	config, err := json.Marshal(oracle.Identity{
		EVMKey:                    "evm_key",
		PeerID:                    "peer_id",
		PublicKey:                 []byte("public_key"),
		OffchainPublicKey:         [32]byte{},
		ConfigEncryptionPublicKey: [32]byte{},
	})
	assert.NoError(t, err)

	assert.NoError(t, capabilitiesServer.Initialise(
		ctx,
		string(config),
		nil, // unused - telemetryService core.TelemetryService
		testutils.NewStore(t),
		testutils.NewCapabilitiesRegistry(t),
		nil, // unused - errorLog core.ErrorLog
		nil, // unused - pipelineRunner core.PipelineRunnerService
		nil, // unused - relayerSet core.RelayerSet
		testutils.NewOracleFactory(t, logger),
	))

	capabilitiesInfos, err := capabilitiesServer.Infos(ctx)
	assert.NoError(t, err)

	fmt.Println(capabilitiesInfos)
	assert.Len(t, capabilitiesInfos, 1)
	assert.Equal(t, "kv-store-target@1.0.0", capabilitiesInfos[0].ID)

	// CapabilityRequest to write to the kvstore
	keyValuePairs := map[string][]byte{
		"key":  []byte("value"),
		"key2": []byte("value2"),
	}
	wrappedKVPairs, err := values.Wrap(keyValuePairs)
	assert.NoError(t, err)

	keyValuePairsBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(values.Proto(wrappedKVPairs))
	assert.NoError(t, err)

	wrappedSignedReport, err := values.Wrap(
		ocr3cap.SignedReport{
			Context:    []uint8{},
			ID:         []uint8{1},
			Report:     keyValuePairsBytes,
			Signatures: [][]uint8{{}},
		},
	)
	assert.NoError(t, err)

	inputs, err := values.NewMap(map[string]any{
		"signedReport": wrappedSignedReport,
	})
	assert.NoError(t, err)

	workflow := testutils.NewWorkflow()
	capabilityRequest := workflow.NewRequest(inputs)
	expectedResponse, err := values.NewMap(map[string]any{
		"success": true,
	})
	assert.NoError(t, err)

	capabilityResponse, err := capabilitiesServer.Target.Execute(ctx, capabilityRequest)
	assert.NoError(t, err)

	assert.Equal(t, capabilities.CapabilityResponse{
		Value: expectedResponse,
	}, capabilityResponse)

	// CapabilityRequest to read from the kvstore
	// TODO: Implement read capability
}
