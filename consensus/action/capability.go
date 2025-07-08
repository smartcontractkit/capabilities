package action

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

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

// TODO - make the batch size configurable via the capability config, possible the batch size could be set based on the
// total size of the requests from the store and limit configured in libocr
const defaultRequestBatchSize = 20

type consensusCapability struct {
	services.StateMachine

	lggr       logger.Logger
	oracle     core.Oracle
	reqStore   *requests.Store[*oracle2.ConsensusRequest]
	reqHandler *requests.Handler[*oracle2.ConsensusRequest, oracle2.ConsensusResponse]

	requestTimeout     time.Duration
	requestTimeoutLock sync.RWMutex

	defaultKeyBundleID string
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
	gatewayConnector core.GatewayConnector, _ core.Keystore) error {

	// TODO key bundle id should be on the request if we want to support multi sig for reports
	c.defaultKeyBundleID = "evm"

	c.lggr.Debugf("Initialising Consensus Capability")

	// TODO get batch size and limits from config
	// TODO check pending requests metric is published

	reportingPlugin, err := oracle2.NewReportingPluginFactory(c.lggr, c.reqStore, c.SetRequestTimeout,
		defaultRequestBatchSize)
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
	// TODO limit check the size of the serialised value and consensus descriptor (and metadata? or can rely on sensible sized values here?), error if too large - pass in the limits
	// in the capability config

	// TODO - workflows request count limits etc - confirm if needs to be handled here

	requestID := metadata.WorkflowExecutionID + "-" + metadata.ReferenceID

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
			lggr.Debugw("neither value, error or default is set in the observation input for request", "requestID", requestID)
		}
	}

	c.requestTimeoutLock.RLock()
	requestTimeout := c.requestTimeout
	c.requestTimeoutLock.RUnlock()

	callbackChan := make(chan oracle2.ConsensusResponse, 1)

	c.reqHandler.SendRequest(ctx,
		oracle2.NewConsensusRequest(requestID, input, time.Now().Add(requestTimeout), callbackChan,
			metadata, c.defaultKeyBundleID,
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
