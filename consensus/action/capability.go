package action

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/exp/maps"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/consensus/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2/types"

	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/capabilities/consensus/oracle/types"

	"github.com/smartcontractkit/capabilities/consensus/oracle"

	"go.opentelemetry.io/otel/metric"
)

const defaultRequestBatchSize = 20
const defaultMaxRequestSizeBytes = 10 * 1024 // 10KB
const defaultKeyBundleIDForValueConsensus = "evm"

const KeyBundleIDEvm = "evm"
const KeyBundleIDAptos = "aptos"
const SigningAlgoEcdsa = "ecdsa"
const HashingAlgoKeccak256 = "keccak256"

type ConsensusCapabilityConfig struct {
	RequestBatchSize             int
	MaxRequestSizeBytes          int
	KeyBundleIDForValueConsensus string
}

var _ server.ConsensusCapability = &consensusCapability{}

type consensusCapability struct {
	services.StateMachine

	lggr       logger.Logger
	oracle     core.Oracle
	reqStore   *requests.Store[*oracle.ConsensusRequest]
	reqHandler *requests.Handler[*oracle.ConsensusRequest, oracle.ConsensusResponse]

	requestTimeout     time.Duration
	requestTimeoutLock sync.RWMutex

	requestBatchSize          int
	maxRequestSizeBytes       int
	valueConsensusKeyBundleID string
}

type storeStatsCollector struct {
	requestStoreRequests metric.Int64Gauge
}

func (s *storeStatsCollector) SetRequestCount(requestCount int) {
	s.requestStoreRequests.Record(context.Background(), int64(requestCount))
}

// NewConsensusCapability creates a new ConsensusCapability with the given logger, clock, and response cache expiry time.  The
// response cache expiry controls how long a response for a given request is cached before it is considered expired and evicted. This allows
// the capability to respond to slow requests sent after consensus has been reached.
func NewConsensusCapability(lggr logger.Logger, clock clockwork.Clock, responseCacheExpiry time.Duration) (*consensusCapability, error) {
	reqStoreGauge := beholder.MetricInfo{
		Name:        "capability_consensus_request_store_requests",
		Unit:        "",
		Description: "The number of requests in the capability consensus request store",
	}

	gauge, err := reqStoreGauge.NewInt64Gauge(beholder.GetMeter())
	if err != nil {
		return nil, fmt.Errorf("failed to create request store gauge: %w", err)
	}

	reqStore := requests.NewStoreWithStatsCollector[*oracle.ConsensusRequest](
		&storeStatsCollector{
			requestStoreRequests: gauge,
		})

	return &consensusCapability{
		lggr:       lggr,
		reqStore:   reqStore,
		reqHandler: requests.NewHandler[*oracle.ConsensusRequest, oracle.ConsensusResponse](lggr, reqStore, clock, responseCacheExpiry),
	}, nil
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
	c.lggr.Debugf("Initialising Consensus Capability")

	if err := c.setConfiguration(config); err != nil {
		return fmt.Errorf("error setting consensus capability configuration: %w", err)
	}

	reportingPlugin, err := oracle.NewReportingPluginFactory(c.lggr, c.reqStore, c.SetRequestTimeout,
		c.requestBatchSize)
	if err != nil {
		return fmt.Errorf("error when creating reporting plugin factory: %w", err)
	}

	contractTransmitter := oracle.NewContractTransmitter(c.lggr, c.SendResponse)

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

func (c *consensusCapability) setConfiguration(config string) error {
	c.valueConsensusKeyBundleID = defaultKeyBundleIDForValueConsensus
	c.requestBatchSize = defaultRequestBatchSize
	c.maxRequestSizeBytes = defaultMaxRequestSizeBytes

	var capabilityConfig ConsensusCapabilityConfig
	if len(config) > 0 {
		err := json.Unmarshal([]byte(config), &capabilityConfig)
		if err != nil {
			return fmt.Errorf("failed to deserialize config into ConsensusCapabilityConfig: %w", err)
		}
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
	return nil
}

func (c *consensusCapability) Simple(ctx context.Context, metadata capabilities.RequestMetadata, input *sdk.SimpleConsensusInputs) (*capabilities.ResponseAndMetadata[*valuespb.Value], error) {
	lggr := c.requestLggr(metadata)

	lggr.Debugw("received simple consensus request", "metadata", metadata, "input", input)

	consensusRequestMetaData := oracle.ConsensusRequestMetadata{
		RequestMetadata: metadata,
		KeyBundleID:     c.valueConsensusKeyBundleID,
		ReportID:        "0000", // Report ID is not used for value consensus, so we can use a dummy value
		RequestType:     types.RequestType_VALUE_CONSENSUS,
	}

	if err := validateRequestSize(consensusRequestMetaData, input, c.maxRequestSizeBytes); err != nil {
		return nil, fmt.Errorf("failed to validate input size: %w", err)
	}

	value, err := logObservation(lggr, input, metadata)
	if err != nil {
		responseAndMetadata := capabilities.ResponseAndMetadata[*valuespb.Value]{
			Response:         value,
			ResponseMetadata: capabilities.ResponseMetadata{},
		}
		return &responseAndMetadata, err
	}

	callbackChan := c.sendRequest(ctx, input, consensusRequestMetaData)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case response := <-callbackChan:
		if response.Err != nil {
			return nil, response.Err
		}

		// Remove the metadata prefix from the raw report to get the serialised value
		serialisedValue := response.RawReport[oracle.ReportMetaDataPrependLength:]

		valueProto := &valuespb.Value{}
		if err := proto.Unmarshal(serialisedValue, valueProto); err != nil {
			return nil, fmt.Errorf("failed to unmarshal value for request %s: %w", consensusRequestMetaData.RequestID(), err)
		}

		c.lggr.Debugw("returning consensus response", "metadata", metadata)

		responseAndMetadata := capabilities.ResponseAndMetadata[*valuespb.Value]{
			Response:         valueProto,
			ResponseMetadata: capabilities.ResponseMetadata{},
		}
		return &responseAndMetadata, nil
	}
}

func (c *consensusCapability) Report(ctx context.Context, metadata capabilities.RequestMetadata, reportRequest *sdk.ReportRequest) (*capabilities.ResponseAndMetadata[*sdk.ReportResponse], error) {
	lggr := c.requestLggr(metadata)

	lggr.Debug("received reporting request", "metadata", metadata)

	stepReferenceID := metadata.ReferenceID
	reportID, err := toReportID(stepReferenceID)
	if err != nil {
		return nil, fmt.Errorf("failed to convert step reference ID to report ID: %w", err)
	}

	keyBundleID, err := validateReportRequest(reportRequest)
	if err != nil {
		return nil, fmt.Errorf("report request validation failed: %w", err)
	}

	consensusRequestMetaData := oracle.ConsensusRequestMetadata{
		RequestMetadata: metadata,
		KeyBundleID:     keyBundleID,
		ReportID:        reportID,
		RequestType:     types.RequestType_REPORT_GENERATION,
	}

	if err := validateRequestSize(consensusRequestMetaData, reportRequest, c.maxRequestSizeBytes); err != nil {
		return nil, fmt.Errorf("failed to validate input size: %w", err)
	}

	input := &sdk.SimpleConsensusInputs{
		Observation: &sdk.SimpleConsensusInputs_Value{
			Value: values.Proto(values.NewBytes(reportRequest.EncodedPayload)),
		},
		Descriptors: &sdk.ConsensusDescriptor{
			Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
				Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL,
			},
		},
		Default: nil,
	}

	callbackChan := c.sendRequest(ctx, input, consensusRequestMetaData)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case response := <-callbackChan:
		if response.Err != nil {
			return nil, response.Err
		}

		var sigs []*sdk.AttributedSignature

		for _, s := range response.Sigs {
			sigs = append(sigs, &sdk.AttributedSignature{
				Signature: s.Signature,
				SignerId:  uint32(s.Signer),
			})
		}

		c.lggr.Debugw("returning report", "metadata", metadata)

		reportResponse := &sdk.ReportResponse{
			ConfigDigest:  response.ConfigDigest[:],
			SeqNr:         response.SeqNr,
			ReportContext: response.ReportContext,
			RawReport:     response.RawReport,
			Sigs:          sigs,
		}
		responseAndMetadata := capabilities.ResponseAndMetadata[*sdk.ReportResponse]{
			Response:         reportResponse,
			ResponseMetadata: capabilities.ResponseMetadata{},
		}
		return &responseAndMetadata, err
	}
}

func (c *consensusCapability) sendRequest(ctx context.Context, input *sdk.SimpleConsensusInputs, consensusRequestMetaData oracle.ConsensusRequestMetadata) <-chan oracle.ConsensusResponse {
	c.requestTimeoutLock.RLock()
	requestTimeout := c.requestTimeout
	c.requestTimeoutLock.RUnlock()

	callbackChan := make(chan oracle.ConsensusResponse, 1)

	c.reqHandler.SendRequest(ctx,
		oracle.NewConsensusRequest(input, time.Now(), time.Now().Add(requestTimeout), callbackChan,
			consensusRequestMetaData,
		))
	return callbackChan
}

func validateReportRequest(reportRequest *sdk.ReportRequest) (string, error) {
	supportedKeyBundleIDs := map[string]struct{}{
		KeyBundleIDEvm:   {},
		KeyBundleIDAptos: {},
	}

	keyBundleID := strings.ToLower(reportRequest.EncoderName)
	if _, ok := supportedKeyBundleIDs[keyBundleID]; !ok {
		return "", fmt.Errorf("unsupported encoder name '%s' for report request. Supported encoder names are: %v", reportRequest.EncoderName, maps.Keys(supportedKeyBundleIDs))
	}

	signingAlgo := strings.ToLower(reportRequest.SigningAlgo)
	supportedSigningAlgorithms := map[string]struct{}{
		SigningAlgoEcdsa: {},
	}

	if _, ok := supportedSigningAlgorithms[signingAlgo]; !ok {
		return "", fmt.Errorf("unsupported signing algorithm '%s' for report request. Supported signing algorithms are: %v", reportRequest.SigningAlgo, maps.Keys(supportedSigningAlgorithms))
	}

	hashingAlgo := strings.ToLower(reportRequest.HashingAlgo)

	supportedHashingAlgorithms := map[string]struct{}{
		HashingAlgoKeccak256: {},
	}

	if _, ok := supportedHashingAlgorithms[hashingAlgo]; !ok {
		return "", fmt.Errorf("unsupported hashing algorithm '%s' for report request. Supported hashing algorithms are: %v", reportRequest.HashingAlgo, maps.Keys(supportedHashingAlgorithms))
	}
	return keyBundleID, nil
}

func toReportID(stepReferenceID string) (string, error) {
	// Parse the step reference to an int
	if stepReferenceID == "" {
		return "", fmt.Errorf("step reference ID is required for report request")
	}

	stepReferenceAsInt, err := strconv.Atoi(stepReferenceID)
	if err != nil {
		return "", fmt.Errorf("failed to parse step reference ID '%s' to int: %w", stepReferenceID, err)
	}

	stepRefAsHex := fmt.Sprintf("%x", stepReferenceAsInt)

	// Pad it with leading zeros so the string is at least 4 characters long
	if len(stepRefAsHex) < 4 {
		padding := 4
		stepRefAsHex = fmt.Sprintf("%0*s", padding, stepRefAsHex) // Pad with zeros to the left
	}

	b, err := hex.DecodeString(stepRefAsHex)
	if err != nil {
		return "", fmt.Errorf("failed to decode step reference ID '%s' to bytes: %w", stepReferenceID, err)
	}

	if len(b) != 2 {
		return "", fmt.Errorf("step reference ID '%s' must be exactly 2 bytes long when encoded as hex, got %d bytes", stepReferenceID, len(b))
	}
	return stepRefAsHex, nil
}

func (c *consensusCapability) requestLggr(metadata capabilities.RequestMetadata) logger.Logger {
	lggr := logger.With(
		c.lggr,
		"workflowID", metadata.WorkflowID,
		"executionID", metadata.WorkflowExecutionID,
		"stepReferenceID", metadata.ReferenceID,
		"workflowName", metadata.DecodedWorkflowName,
	)
	return lggr
}

func (c *consensusCapability) SendResponse(ctx context.Context, response oracle.ConsensusResponse) {
	c.reqHandler.SendResponse(ctx, response)
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

// validateRequestSize ensures the combined size of input and metadata does not exceed the allowed limit.
// This prevents oversized requests that could disrupt the consensus process.
func validateRequestSize(consensusRequestMetaData oracle.ConsensusRequestMetadata, input proto.Message, maxRequestSizeBytes int) error {
	requestMetaData := oracle.ToRequestMetaData(consensusRequestMetaData)

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

func logObservation(lggr logger.Logger, input *sdk.SimpleConsensusInputs, metadata capabilities.RequestMetadata) (*valuespb.Value, error) {
	switch obs := input.GetObservation().(type) {
	case *sdk.SimpleConsensusInputs_Value:
		val, err := values.FromProto(obs.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to decode observation value: %w", err)
		}
		lggr.Debugw("received observation value", "value", val)
	case *sdk.SimpleConsensusInputs_Error:
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
	return nil, nil
}
