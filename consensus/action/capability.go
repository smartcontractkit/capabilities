package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk/v2/pb"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2/types"

	oracle2 "github.com/smartcontractkit/capabilities/consensus/oracle"
)

const (
	defaultRequestBatchSize             = 20
	defaultMaxRequestSizeBytes          = 10 * 1024 // 10KB
	defaultKeyBundleIDForValueConsensus = "evm"
)

type ConsensusCapabilityConfig struct {
	RequestBatchSize             int
	MaxRequestSizeBytes          int
	KeyBundleIDForValueConsensus string
}

type consensusCapability struct {
	services.StateMachine

	lggr       logger.Logger
	oracle     core.Oracle
	reqStore   *requests.Store[*oracle2.ConsensusRequest]
	reqHandler *requests.Handler[*oracle2.ConsensusRequest, oracle2.ConsensusResponse]

	requestTimeout     time.Duration
	requestTimeoutLock sync.RWMutex

	requestBatchSize          int
	maxRequestSizeBytes       int
	valueConsensusKeyBundleID string
}

// NewConsensusCapability creates a new ConsensusCapability with the given logger, clock, and response cache expiry time.  The
// response cache expiry controls how long a response for a given request is cached before it is considered expired and evicted. This allows
// the capability to respond to slow requests sent after consensus has been reached.
func NewConsensusCapability(lggr logger.Logger, clock clockwork.Clock, responseCacheExpiry time.Duration) *consensusCapability {
	reqStore := requests.NewStore[*oracle2.ConsensusRequest]()

	return &consensusCapability{
		lggr:       lggr,
		reqStore:   reqStore,
		reqHandler: requests.NewHandler[*oracle2.ConsensusRequest, oracle2.ConsensusResponse](lggr, reqStore, clock, responseCacheExpiry),
	}
}

// SetRequestTimeout is used by the reporting plugin to set the request timeout for consensus requests.  The plugin
// receives the timeout after the Initialise method on the capability is called, thus it uses this method to set it on the capability.
func (c *consensusCapability) SetRequestTimeout(timeout time.Duration) {
	c.requestTimeoutLock.Lock()
	defer c.requestTimeoutLock.Unlock()
	c.requestTimeout = timeout
}

func (c *consensusCapability) Initialise(ctx context.Context, config string,
	telemetryService core.TelemetryService,
	store core.KeyValueStore, errorLog core.ErrorLog, pipelineRunner core.PipelineRunnerService,
	relayerSet core.RelayerSet, oracleFactory core.OracleFactory,
	gatewayConnector core.GatewayConnector, _ core.Keystore,
) error {
	c.lggr.Debugf("Initialising Consensus Capability")

	c.valueConsensusKeyBundleID = defaultKeyBundleIDForValueConsensus
	c.requestBatchSize = defaultRequestBatchSize
	c.maxRequestSizeBytes = defaultMaxRequestSizeBytes

	var capabilityConfig ConsensusCapabilityConfig
	err := json.Unmarshal([]byte(config), &capabilityConfig)
	if err != nil {
		return fmt.Errorf("failed to deserialize config into ConsensusCapabilityConfig: %w", err)
	}

	c.lggr.Debugw("Parsed Consensus Capability config", "config", capabilityConfig)

	if len(capabilityConfig.KeyBundleIDForValueConsensus) > 0 {
		c.valueConsensusKeyBundleID = capabilityConfig.KeyBundleIDForValueConsensus
	}

	if capabilityConfig.RequestBatchSize > 0 {
		c.requestBatchSize = capabilityConfig.RequestBatchSize
	}

	if capabilityConfig.MaxRequestSizeBytes > 0 {
		c.maxRequestSizeBytes = capabilityConfig.MaxRequestSizeBytes
	}

	c.lggr.Debugf("Initialising Consensus Capability")

	// TODO check pending requests metric is published

	reportingPlugin, err := oracle2.NewReportingPluginFactory(c.lggr, c.reqStore, c.SetRequestTimeout,
		c.requestBatchSize)
	if err != nil {
		return fmt.Errorf("error when creating reporting plugin factory: %w", err)
	}

	contractTransmitter := oracle2.NewContractTransmitter(c.lggr, c.SendResponse)

	// These values set to the maximum permitted, response time for config update is not critical
	localOcrConfig := ocrtypes.LocalConfig{
		BlockchainTimeout:                  time.Second * 20,
		ContractConfigTrackerPollInterval:  time.Second * 60,
		ContractConfigConfirmations:        1,
		ContractTransmitterTransmitTimeout: time.Second * 60,
		DatabaseTimeout:                    time.Second * 10,
		ContractConfigLoadTimeout:          time.Second * 60,
		DefaultMaxDurationInitialization:   time.Second * 60,
	}

	oracle, err := oracleFactory.NewOracle(ctx, core.OracleArgs{
		LocalConfig: localOcrConfig,

		ReportingPluginFactoryService: reportingPlugin,
		ContractTransmitter:           contractTransmitter,
	})
	if err != nil {
		return fmt.Errorf("error when creating oracle: %w", err)
	}

	c.oracle = oracle
	err = c.reqHandler.Start(context.Background())
	if err != nil {
		return fmt.Errorf("error when starting request handler: %w", err)
	}

	err = c.oracle.Start(context.Background())
	if err != nil {
		return fmt.Errorf("error when starting oracle: %w", err)
	}

	c.lggr.Debug("Initialised Consensus Capability")

	return nil
}

func (c *consensusCapability) Simple(ctx context.Context, metadata capabilities.RequestMetadata, input *pb.SimpleConsensusInputs) (*valuespb.Value, error) {
	consensusRequestMetaData := oracle2.ConsensusRequestMetadata{
		RequestMetadata: metadata,
		KeyBundleID:     c.valueConsensusKeyBundleID,
	}

	if err := validateInputSize(consensusRequestMetaData, input, c.maxRequestSizeBytes); err != nil {
		return nil, fmt.Errorf("failed to validate input size: %w", err)
	}

	lggr := logger.With(
		c.lggr,
		"workflowID", metadata.WorkflowID,
		"executionID", metadata.WorkflowExecutionID,
		"stepReferenceID", metadata.ReferenceID,
		"workflowName", metadata.DecodedWorkflowName,
	)

	switch obs := input.GetObservation().(type) {
	case *pb.SimpleConsensusInputs_Value:
		val, err := values.FromProto(obs.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to decode observation value: %w", err)
		}
		lggr.Debugw("received observation value", "value", val)
	case *pb.SimpleConsensusInputs_Error:
		lggr.Debugw("observation is an error", "error", obs.Error)
	default:
		if input.Default != nil {
			val, err := values.FromProto(input.Default)
			if err != nil {
				return nil, fmt.Errorf("failed to decode observation value: %w", err)
			}
			lggr.Debugw("serialised default value", "value", val)
		} else {
			lggr.Debugw("neither value, error or default is set in the observation input for request", "metadata", metadata)
		}
	}

	c.requestTimeoutLock.RLock()
	requestTimeout := c.requestTimeout
	c.requestTimeoutLock.RUnlock()

	callbackChan := make(chan oracle2.ConsensusResponse, 1)

	c.reqHandler.SendRequest(ctx,
		oracle2.NewConsensusRequest(input, time.Now().Add(requestTimeout), callbackChan,
			consensusRequestMetaData,
		))

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case response := <-callbackChan:
		if response.Err != nil {
			return nil, response.Err
		}

		return response.Value, nil
	}
}

func (c *consensusCapability) Report(ctx context.Context, metadata capabilities.RequestMetadata, input *pb.ReportRequest) (*pb.ReportResponse, error) {
	// TODO
	return nil, errors.New("report method is not implemented for Consensus Capability")
}

func (c *consensusCapability) SendResponse(ctx context.Context, requestID string, value *valuespb.Value) {
	c.reqHandler.SendResponse(ctx, oracle2.ConsensusResponse{
		ReqID: requestID,
		Value: value,
	})
}

// Start is not called when running as remote standard capability, instead Initialise is called  (note Close is called)
func (c *consensusCapability) Start(ctx context.Context) error {
	return nil
}

func (c *consensusCapability) Close() error {
	err := c.reqHandler.Close()
	if err != nil {
		c.lggr.Errorw("error closing request handler", "err", err)
	}

	if c.oracle != nil {
		if err := c.oracle.Close(context.Background()); err != nil {
			return fmt.Errorf("error when closing oracle: %w", err)
		}
		c.oracle = nil
	}

	return nil
}

func (c *consensusCapability) Ready() error {
	return nil
}

func (c *consensusCapability) HealthReport() map[string]error {
	return map[string]error{c.Name(): nil}
}

func (c *consensusCapability) Name() string {
	return c.lggr.Name()
}

func (c *consensusCapability) Description() string {
	return "Consensus Capability"
}

// validateInputSize checks that the size of the input and metadata does not exceed the maximum request size.  This is to
// prevent excessively large requests that could cause issues in the consensus process.
func validateInputSize(consensusRequestMetaData oracle2.ConsensusRequestMetadata, input *pb.SimpleConsensusInputs, maxRequestSizeBytes int) error {
	requestMetaData := oracle2.ToRequestMetaData(consensusRequestMetaData)

	serialisedInput, err := proto.Marshal(input)
	if err != nil {
		return fmt.Errorf("failed to serialise input: %w", err)
	}

	serialisedMetadata, err := proto.Marshal(requestMetaData)
	if err != nil {
		return fmt.Errorf("failed to serialise metadata: %w", err)
	}

	if len(serialisedInput)+len(serialisedMetadata) > maxRequestSizeBytes {
		return fmt.Errorf("request size exceeds maximum allowed size of %d bytes: got %d bytes", maxRequestSizeBytes, len(serialisedInput)+len(serialisedMetadata))
	}
	return nil
}
