package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/libs/testutils"

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

	// Timeout is important to avoid hanging tests
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assert.NoError(t, capabilitiesServer.Start(ctx))

	assert.NoError(t, capabilitiesServer.Initialise(
		ctx,
		"",  // unused - empty config
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

	assert.Len(t, capabilitiesInfos, 2)
	assert.Equal(t, "kv-store-action@1.0.0", capabilitiesInfos[0].ID)
	assert.Equal(t, "kv-store-target@1.0.0", capabilitiesInfos[1].ID)

	workflow := testutils.NewWorkflow(t)

	// CapabilityRequest to write to the kvstore
	wrappedKVPairs, err := values.Wrap(map[string][]byte{
		"key":  []byte("value"),
		"key2": []byte("value2"),
	})
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

	response, err := capabilitiesServer.Target.Execute(ctx, workflow.NewRequest(map[string]any{
		"signedReport": wrappedSignedReport,
	}))
	assert.NoError(t, err)

	assert.Equal(t, workflow.NewResponse(map[string]any{
		"success": true,
	}), response)

	// CapabilityRequest to read from the kvstore
	response, err = capabilitiesServer.Action.Execute(ctx, workflow.NewRequest(map[string]any{
		"Keys": []string{"key", "key2", "key3"},
	}))
	assert.NoError(t, err)

	assert.Equal(t, workflow.NewResponse(map[string]any{
		"key":  []byte("value"),
		"key2": []byte("value2"),
		"key3": []byte(""),
	}), response)

	// Delete from the kvstore
	wrappedKVPairs, err = values.Wrap(map[string][]byte{
		"key":  []byte(""),
		"key3": []byte("foo"),
	})
	assert.NoError(t, err)

	keyValuePairsBytes, err = proto.MarshalOptions{Deterministic: true}.Marshal(values.Proto(wrappedKVPairs))
	assert.NoError(t, err)

	wrappedSignedReport, err = values.Wrap(
		ocr3cap.SignedReport{
			Context:    []uint8{},
			ID:         []uint8{1},
			Report:     keyValuePairsBytes,
			Signatures: [][]uint8{{}},
		},
	)
	assert.NoError(t, err)

	response, err = capabilitiesServer.Target.Execute(ctx, workflow.NewRequest(map[string]any{
		"signedReport": wrappedSignedReport,
	}))
	assert.NoError(t, err)

	assert.Equal(t, workflow.NewResponse(map[string]any{
		"success": true,
	}), response)

	// CapabilityRequest to read final values
	response, err = capabilitiesServer.Action.Execute(ctx, workflow.NewRequest(map[string]any{
		"Keys": []string{"key", "key2", "key3"},
	}))
	assert.NoError(t, err)

	assert.Equal(t, workflow.NewResponse(map[string]any{
		"key":  []byte(""),
		"key2": []byte("value2"),
		"key3": []byte("foo"),
	}), response)
}
