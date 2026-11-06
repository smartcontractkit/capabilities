package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/capabilities/internal/consensus/oracle"
	"github.com/smartcontractkit/capabilities/internal/consensus/oracle/types"
	"github.com/smartcontractkit/capabilities/internal/consensus/protos"
	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/capabilitytest"
)

// startConsensus starts one instance of the consensus capability as capability.Run would, with cfg.
func startConsensus(t *testing.T, cfg Config) *protos.ConsensusServer {
	t.Helper()

	return capabilitytest.Start(t, New, 1, capability.WithProvider(func(i *capability.Injector) error {
		return i.ProvideValue(&cfg)
	}))[0]
}

func TestConsensus(t *testing.T) {
	c := startConsensus(t, Config{})

	t.Run("simple", func(t *testing.T) {
		result, err := c.Simple(t.Context(), newRequestMetaData(), median(values.Proto(values.NewInt64(10))))
		require.NoError(t, err)
		assert.True(t, proto.Equal(values.Proto(values.NewInt64(10)), result.Response), "got %v", result.Response)
	})

	t.Run("simple error observation", func(t *testing.T) {
		input := &sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Error{Error: "its broken"},
			Descriptors: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN},
			},
		}

		_, err := c.Simple(t.Context(), newRequestMetaData(), input)
		requireConsensusFailed(t, err)
		assert.Equal(t, caperrors.OriginUser, err.Origin())
		assert.Equal(t, caperrors.VisibilityPublic, err.Visibility())
	})

	for _, encoder := range []struct{ name, signingAlgo, hashingAlgo string }{
		{name: "evm", signingAlgo: "ecdsa", hashingAlgo: "keccak256"},
		{name: "aptos", signingAlgo: "ed25519", hashingAlgo: "blake2b_256"},
		{name: "stellar", signingAlgo: "ed25519", hashingAlgo: "blake2b_256"},
	} {
		t.Run("report/"+encoder.name, func(t *testing.T) {
			result, err := c.Report(t.Context(), newRequestMetaData(), &sdk.ReportRequest{
				EncodedPayload: []byte("somerandom-payload"),
				EncoderName:    encoder.name,
				SigningAlgo:    encoder.signingAlgo,
				HashingAlgo:    encoder.hashingAlgo,
			})
			require.NoError(t, err)
			assert.True(t, strings.HasSuffix(string(result.Response.RawReport), "somerandom-payload"))
		})
	}

	for _, invalid := range []struct {
		name    string
		request *sdk.ReportRequest
		err     string
	}{
		{
			name:    "signing algorithm",
			request: &sdk.ReportRequest{EncoderName: "evm", SigningAlgo: "invalid-signing-algo", HashingAlgo: "keccak256"},
			err:     "unsupported signing algorithm",
		},
		{
			name:    "hashing algorithm",
			request: &sdk.ReportRequest{EncoderName: "evm", SigningAlgo: "ecdsa", HashingAlgo: "invalid-hashing-algo"},
			err:     "unsupported hashing algorithm",
		},
		{
			name:    "encoder name",
			request: &sdk.ReportRequest{EncoderName: "invalid-encoder-name", SigningAlgo: "ecdsa", HashingAlgo: "keccak256"},
			err:     "unsupported encoder name",
		},
	} {
		t.Run("report rejects invalid "+invalid.name, func(t *testing.T) {
			_, err := c.Report(t.Context(), newRequestMetaData(), invalid.request)
			require.ErrorContains(t, err, invalid.err)
		})
	}
}

// TestConsensus_OversizedRequestFailsConsensus checks a request over the size limit is observed as a user error, so
// every node observes it and consensus fails.
func TestConsensus_OversizedRequestFailsConsensus(t *testing.T) {
	for _, tc := range []struct {
		name                string
		maxRequestSizeBytes int
		observation         string
	}{
		{name: "tiny limit", maxRequestSizeBytes: 2, observation: "34"},
		{name: "large limit", maxRequestSizeBytes: 2_000_000, observation: strings.Repeat("x", 2_100_000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := startConsensus(t, Config{MaxRequestSizeBytes: tc.maxRequestSizeBytes})
			input := &sdk.SimpleConsensusInputs{
				Observation: &sdk.SimpleConsensusInputs_Value{Value: values.Proto(values.NewString(tc.observation))},
				Descriptors: &sdk.ConsensusDescriptor{
					Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL},
				},
			}

			_, err := c.Simple(t.Context(), newRequestMetaData(), input)
			requireConsensusFailed(t, err)
		})
	}
}

func TestSendRequest_ActivePeriodFlags(t *testing.T) {
	activeFrom := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	activeUntil := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	activePeriod := settings.Range[config.Timestamp]{
		Lower: config.NewTimestamp(activeFrom),
		Upper: config.NewTimestamp(activeUntil),
	}

	flags := []struct {
		name    string
		set     func(c *consensusCapability, limiter limits.RangeLimiter[config.Timestamp])
		get     func(req *oracle.ConsensusRequest) bool
		warning string
	}{
		{
			name: "stricterMedianQuorum",
			set: func(c *consensusCapability, limiter limits.RangeLimiter[config.Timestamp]) {
				c.stricterMedianQuorum = limiter
			},
			get:     func(req *oracle.ConsensusRequest) bool { return req.StricterMedianQuorum },
			warning: "error evaluating stricterMedianQuorum, defaulting to off",
		},
		{
			name: "includeAllTimestamps",
			set: func(c *consensusCapability, limiter limits.RangeLimiter[config.Timestamp]) {
				c.includeAllTimestamps = limiter
			},
			get:     func(req *oracle.ConsensusRequest) bool { return req.IncludeAllTimestamps },
			warning: "error evaluating includeAllTimestamps, defaulting to off",
		},
	}

	testCases := []struct {
		name               string
		executionTimestamp time.Time
		closeLimiter       bool
		expectedFlag       bool
		expectWarning      bool
	}{
		{name: "inside the active period", executionTimestamp: activeFrom.Add(time.Hour), expectedFlag: true},
		{name: "before the active period", executionTimestamp: activeFrom.Add(-time.Hour)},
		{name: "after the active period", executionTimestamp: activeUntil.Add(time.Hour)},
		{name: "unset execution timestamp", executionTimestamp: time.Time{}},
		{name: "limiter error defaults to off", executionTimestamp: activeFrom.Add(time.Hour), closeLimiter: true, expectWarning: true},
	}

	for _, flag := range flags {
		for _, tc := range testCases {
			t.Run(flag.name+"/"+tc.name, func(t *testing.T) {
				lggr, logs := logger.TestObserved(t, zapcore.WarnLevel)
				c, err := newConsensusCapability(lggr, clockwork.NewRealClock(), limits.Factory{Logger: lggr}, Config{}, nil)
				require.NoError(t, err)

				limiter := limits.NewRangeLimiter(activePeriod)
				if tc.closeLimiter {
					require.NoError(t, limiter.Close())
				}

				flag.set(c, limiter)
				servicetest.Run(t, c.reqHandler)

				metadata := newRequestMetaData()
				metadata.ExecutionTimestamp = tc.executionTimestamp
				md := oracle.ConsensusRequestMetadata{RequestMetadata: metadata, RequestType: types.RequestType_VALUE_CONSENSUS}
				c.sendRequest(t.Context(), &sdk.SimpleConsensusInputs{}, md)

				var req *oracle.ConsensusRequest
				require.Eventually(t, func() bool {
					req = c.reqStore.Get(md.RequestID())
					return req != nil
				}, 5*time.Second, 10*time.Millisecond)

				assert.Equal(t, tc.expectedFlag, flag.get(req))
				if tc.expectWarning {
					assert.Equal(t, 1, logs.FilterMessage(flag.warning).Len())
				} else {
					assert.Zero(t, logs.FilterMessage(flag.warning).Len())
				}
			})
		}
	}
}

func Test_ToReportID(t *testing.T) {
	tests := []struct {
		name            string
		stepReferenceID string
		expectedID      string
		expectError     bool
	}{
		{name: "Valid step reference ID", stepReferenceID: "0", expectedID: "0000"},
		{name: "Valid step reference ID", stepReferenceID: "1", expectedID: "0001"},
		{name: "Valid step reference ID", stepReferenceID: "26", expectedID: "001a"},
		{name: "Valid step reference ID", stepReferenceID: "614", expectedID: "0266"},
		{name: "Valid step reference ID", stepReferenceID: "65535", expectedID: "ffff"},
		{name: "Empty step reference ID", stepReferenceID: "", expectError: true},
		{name: "Non-numeric step reference ID", stepReferenceID: "abc", expectError: true},
		{name: "Step reference ID too large", stepReferenceID: "65536", expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := toReportID(tt.stepReferenceID)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedID, result)
			}
		})
	}
}

func median(value *valuespb.Value) *sdk.SimpleConsensusInputs {
	return &sdk.SimpleConsensusInputs{
		Observation: &sdk.SimpleConsensusInputs_Value{Value: value},
		Descriptors: &sdk.ConsensusDescriptor{
			Descriptor_: &sdk.ConsensusDescriptor_Aggregation{Aggregation: sdk.AggregationType_AGGREGATION_TYPE_MEDIAN},
		},
	}
}

func requireConsensusFailed(t *testing.T, err error) {
	t.Helper()

	capErr, ok := errors.AsType[caperrors.Error](err)
	require.True(t, ok, "error should be a caperrors.Error, got %v", err)
	require.Equal(t, caperrors.ConsensusFailed, capErr.Code())
}

// newRequestMetaData is the metadata of a request with an execution of its own.
func newRequestMetaData() capabilities.RequestMetadata {
	executionID := make([]byte, 32)
	_, _ = rand.Read(executionID)
	return capabilities.RequestMetadata{
		WorkflowID:               "0039525c34de895c8fa68006bd63f6ce4a45ef1bc66377e791c6a8ae803dc0e4",
		WorkflowOwner:            "1139525c34de895c8fa68006bd634387a9f1192a",
		WorkflowExecutionID:      hex.EncodeToString(executionID),
		WorkflowName:             "a1b2c3d4e5f6a1b2c3d4",
		WorkflowDonID:            1,
		WorkflowDonConfigVersion: 1,
		ReferenceID:              "01",
		DecodedWorkflowName:      "test-workflow-decoded",
	}
}
