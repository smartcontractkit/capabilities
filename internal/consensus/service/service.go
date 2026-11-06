package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/prometheus/client_golang/prometheus"

	"golang.org/x/exp/maps"

	"google.golang.org/protobuf/proto"

	"go.opentelemetry.io/otel/metric"

	"github.com/smartcontractkit/capabilities/internal/consensus/metrics"
	"github.com/smartcontractkit/capabilities/internal/consensus/oracle"
	"github.com/smartcontractkit/capabilities/internal/consensus/oracle/plugin"
	"github.com/smartcontractkit/capabilities/internal/consensus/oracle/transmitter"
	"github.com/smartcontractkit/capabilities/internal/consensus/oracle/types"
	"github.com/smartcontractkit/capabilities/internal/consensus/protos"
	"github.com/smartcontractkit/capabilities/libs/ocr"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/requests"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	commonocr "github.com/smartcontractkit/chainlink-common/pkg/capabilities/ocr"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/ocr/remote"
	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"

	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2/types"
	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
)

const (
	defaultMaxRequestOutcomeSize        = 10000
	defaultKeyBundleIDForValueConsensus = "evm"
	KeyBundleIDEvm                      = "evm"
	KeyBundleIDAptos                    = "aptos"
	KeyBundleIDSolana                   = "solana"
	KeyBundleIDStellar                  = "stellar"
	SigningAlgoEcdsa                    = "ecdsa"
	SigningAlgoEd25519                  = "ed25519"
	HashingAlgoKeccak256                = "keccak256"
	HashingAlgoBlake2b256               = "blake2b_256"
)

type Config struct {
	// RequestBatchSize is unused, and kept so existing job configs still parse.
	RequestBatchSize int
	// MaxRequestSizeBytes is the largest request, observation and metadata together. Unset uses the CRE settings
	// default, and neither may exceed libocr's largest observation.
	MaxRequestSizeBytes int
	// KeyBundleIDForValueConsensus is the key bundle value consensus reports are signed with. Unset uses evm.
	KeyBundleIDForValueConsensus string
	// MaxRequestOutcomeSize is the largest outcome of one request. Unset uses 10000.
	MaxRequestOutcomeSize int
}

var _ protos.ConsensusCapability = &consensusCapability{}

type consensusCapability struct {
	lggr          logger.Logger
	deps          remote.Dependencies
	newOracle     func(libocr.OracleArgs) (libocr.Oracle, error)
	oracle        libocr.Oracle
	reqStore      *requests.Store[*oracle.ConsensusRequest]
	reqHandler    *requests.Handler[*oracle.ConsensusRequest, oracle.ConsensusResponse]
	limitsFactory limits.Factory

	observationQuorumTracker *oracle.ObservationQuorumTracker

	requestTimeout     time.Duration
	requestTimeoutLock sync.RWMutex

	maxRequestSizeBytes       limits.BoundLimiter[config.Size]
	stricterMedianQuorum      limits.RangeLimiter[config.Timestamp]
	includeAllTimestamps      limits.RangeLimiter[config.Timestamp]
	valueConsensusKeyBundleID string
	maxRequestOutcomeSize     int

	metrics *metrics.Metrics
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
func newConsensusCapability(lggr logger.Logger, clock clockwork.Clock, responseCacheExpiry time.Duration,
	limitsFactory limits.Factory,
) (*consensusCapability, error) {
	metrics, err := metrics.NewMetrics()
	if err != nil {
		return nil, fmt.Errorf("error creating metrics: %w", err)
	}

	reqStore := requests.NewStoreWithStatsCollector[*oracle.ConsensusRequest](
		&storeStatsCollector{
			requestStoreRequests: metrics.PendingConsensusRequests,
		})

	stricterMedianQuorum, err := limits.MakeRangeLimiter(limitsFactory, cresettings.Default.PerWorkflow.FeatureConsensusStricterMedianQuorumActivePeriod)
	if err != nil {
		return nil, fmt.Errorf("error creating stricter median quorum limiter: %w", err)
	}

	includeAllTimestamps, err := limits.MakeRangeLimiter(limitsFactory, cresettings.Default.PerWorkflow.FeatureConsensusIncludeAllTimestampsActivePeriod)
	if err != nil {
		return nil, fmt.Errorf("error creating include all timestamps limiter: %w", err)
	}

	return &consensusCapability{
		lggr:                     lggr,
		reqStore:                 reqStore,
		reqHandler:               requests.NewHandler(lggr, reqStore, clock, responseCacheExpiry),
		metrics:                  metrics,
		limitsFactory:            limitsFactory,
		stricterMedianQuorum:     stricterMedianQuorum,
		includeAllTimestamps:     includeAllTimestamps,
		observationQuorumTracker: oracle.NewObservationQuorumTracker(),
		newOracle:                libocr.NewOracle,
	}, nil
}

// SetRequestTimeout is used by the reporting plugin to set the request timeout for consensus requests.  The plugin
// receives the timeout after the Initialise method on the capability is called, thus it uses this method to set it on the capability.
func (c *consensusCapability) SetRequestTimeout(timeout time.Duration) {
	c.requestTimeoutLock.Lock()
	defer c.requestTimeoutLock.Unlock()
	c.requestTimeout = timeout
}

// Configure sets the capability's config and the node's OCR its oracle runs over. It must be called before Start.
func (c *consensusCapability) Configure(cfg Config, deps remote.Dependencies) error {
	if err := c.setConfiguration(cfg); err != nil {
		return fmt.Errorf("error setting consensus capability configuration: %w", err)
	}

	c.deps = deps
	return nil
}

// Start makes the capability's oracle and starts it.
func (c *consensusCapability) Start(ctx context.Context) error {
	if c.maxRequestSizeBytes == nil {
		return errors.New("the consensus capability must be configured before it is started")
	}

	reportingPlugin, err := plugin.NewReportingPluginFactory(c.lggr, c.metrics, c.reqStore,
		c.observationQuorumTracker,
		c.SetRequestTimeout,
		defaultKeyBundleIDForValueConsensus, c.maxRequestOutcomeSize)
	if err != nil {
		return fmt.Errorf("error when creating reporting plugin factory: %w", err)
	}

	contractTransmitter := transmitter.NewContractTransmitter(c.lggr, c.SendResponse)

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

	oracle, err := c.newOracle(ocr.WithNode(libocr.OCR3OracleArgs2[plugin.ReportInfo]{
		LocalConfig:            localOcrConfig,
		ReportingPluginFactory: reportingPlugin,
		ContractTransmitter:    contractTransmitter,
		Database:               commonocr.NewMemoryDB(c.Name(), c.lggr),
		Logger:                 logger.NewOCRWrapper(c.lggr, true, func(string) {}),
		MetricsRegisterer:      prometheus.DefaultRegisterer,
	}, c.deps))
	if err != nil {
		return fmt.Errorf("error when creating oracle: %w", err)
	}

	c.oracle = oracle
	err = c.reqHandler.Start(context.Background())
	if err != nil {
		return fmt.Errorf("error when starting request handler: %w", err)
	}

	err = c.oracle.Start()
	if err != nil {
		return fmt.Errorf("error when starting oracle: %w", err)
	}

	c.lggr.Debug("Started Consensus Capability")

	return nil
}

func (c *consensusCapability) setConfiguration(capabilityConfig Config) error {
	c.valueConsensusKeyBundleID = defaultKeyBundleIDForValueConsensus

	if capabilityConfig.MaxRequestOutcomeSize > 0 {
		c.maxRequestOutcomeSize = capabilityConfig.MaxRequestOutcomeSize
	} else {
		c.maxRequestOutcomeSize = defaultMaxRequestOutcomeSize
	}

	c.lggr.Debugw("Parsed Consensus Capability config", "config", capabilityConfig)

	if len(capabilityConfig.KeyBundleIDForValueConsensus) > 0 {
		c.valueConsensusKeyBundleID = capabilityConfig.KeyBundleIDForValueConsensus
	}

	// Get the limit from CRE settings or capability config
	requestSizeLimit := cresettings.Default.PerWorkflow.Consensus.ObservationSizeLimit // make a copy

	var configuredLimit config.Size
	if capabilityConfig.MaxRequestSizeBytes > 0 {
		// Use capability config if explicitly set
		configuredLimit = config.Size(capabilityConfig.MaxRequestSizeBytes)
	} else {
		// Otherwise use CRE settings default
		configuredLimit = requestSizeLimit.DefaultValue
	}

	// Cap the limit at libOCR's MaxMaxObservationLength to ensure it never exceeds OCR protocol limits
	libOCRLimit := config.Size(int(ocr3types.MaxMaxObservationLength))
	if configuredLimit > libOCRLimit {
		c.lggr.Warnw("Request size limit exceeds libOCR maximum, capping at libOCR limit",
			"configuredLimit", configuredLimit,
			"libOCRLimit", libOCRLimit)
		configuredLimit = libOCRLimit
	}

	requestSizeLimit.DefaultValue = configuredLimit
	maxRequestSizeBytes, err := limits.MakeBoundLimiter(c.limitsFactory, requestSizeLimit)
	if err != nil {
		return err
	}
	c.maxRequestSizeBytes = maxRequestSizeBytes
	return nil
}

func (c *consensusCapability) Simple(ctx context.Context, metadata capabilities.RequestMetadata, input *sdk.SimpleConsensusInputs) (*capabilities.ResponseAndMetadata[*valuespb.Value], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)
	lggr := c.requestLggr(metadata)

	if err := decodeObservationType(lggr, input); err != nil {
		responseAndMetadata := capabilities.ResponseAndMetadata[*valuespb.Value]{}
		return &responseAndMetadata, caperrors.NewPublicSystemError(fmt.Errorf("failed to decode observation: %w", err), caperrors.InvalidArgument)
	}
	if err := validateSimpleConsensusInput(input); err != nil {
		responseAndMetadata := capabilities.ResponseAndMetadata[*valuespb.Value]{}
		return &responseAndMetadata, caperrors.NewPublicSystemError(fmt.Errorf("invalid consensus input: %w", err), caperrors.InvalidArgument)
	}

	lggr.Debugw("received simple consensus request", "metadata", metadata)

	consensusRequestMetaData := oracle.ConsensusRequestMetadata{
		RequestMetadata: metadata,
		KeyBundleID:     c.valueConsensusKeyBundleID,
		ReportID:        "0000", // Report ID is not used for value consensus, so we can use a dummy value
		RequestType:     types.RequestType_VALUE_CONSENSUS,
	}

	requestSize, reqSizeErr := c.validateRequestSize(ctx, lggr, consensusRequestMetaData, input)
	if reqSizeErr != nil {
		var capErr caperrors.Error
		if errors.As(reqSizeErr, &capErr) && capErr.Origin() != caperrors.OriginUser {
			// Return system errors immediately
			lggr.Errorw("failed to validate request size", "err", reqSizeErr)
			return nil, reqSizeErr
		}

		lggr.Debugw("request size validation failed with user error, sending as error input to consensus",
			"err", reqSizeErr,
			"requestSize", requestSize)

		input = &sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Error{
				Error: reqSizeErr.Error(),
			},
			Descriptors: input.Descriptors,
			Default:     input.Default,
		}
	}

	c.metrics.RecordRequestSize(ctx, float64(requestSize))

	callbackChan := c.sendRequest(ctx, input, consensusRequestMetaData)

	select {
	case <-ctx.Done():
		return nil, caperrors.NewPublicSystemError(ctx.Err(), caperrors.Canceled)
	case response := <-callbackChan:
		if response.Err != nil {
			return nil, response.Err
		}

		// Remove the metadata prefix from the raw report to get the serialised value
		serialisedValue := response.RawReport[plugin.ReportMetaDataPrependLength:]

		valueProto := &valuespb.Value{}
		if err := proto.Unmarshal(serialisedValue, valueProto); err != nil {
			return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to unmarshal value for request %s: %w", consensusRequestMetaData.RequestID(), err),
				caperrors.Internal)
		}

		c.lggr.Debugw("returning consensus response", "metadata", metadata)

		responseAndMetadata := capabilities.ResponseAndMetadata[*valuespb.Value]{
			Response:         valueProto,
			ResponseMetadata: capabilities.ResponseMetadata{},
		}
		return &responseAndMetadata, nil
	}
}

func (c *consensusCapability) Report(ctx context.Context, metadata capabilities.RequestMetadata, reportRequest *sdk.ReportRequest) (*capabilities.ResponseAndMetadata[*sdk.ReportResponse], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)
	lggr := c.requestLggr(metadata)

	lggr.Debug("received reporting request", "metadata", metadata)

	stepReferenceID := metadata.ReferenceID
	reportID, err := toReportID(stepReferenceID)
	if err != nil {
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("failed to convert step reference ID to report ID: %w", err), caperrors.Internal)
	}

	keyBundleID, err := validateReportRequest(reportRequest)
	if err != nil {
		return nil, caperrors.NewPublicUserError(fmt.Errorf("report request validation failed: %w", err), caperrors.InvalidArgument)
	}

	consensusRequestMetaData := oracle.ConsensusRequestMetadata{
		RequestMetadata: metadata,
		KeyBundleID:     keyBundleID,
		ReportID:        reportID,
		RequestType:     types.RequestType_REPORT_GENERATION,
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

	requestSize, reqSizeErr := c.validateRequestSize(ctx, lggr, consensusRequestMetaData, reportRequest)
	if reqSizeErr != nil {
		var capErr caperrors.Error
		if errors.As(reqSizeErr, &capErr) && capErr.Origin() != caperrors.OriginUser {
			// Return system errors immediately
			lggr.Errorw("failed to validate request size", "err", reqSizeErr)
			return nil, reqSizeErr
		}
		lggr.Debugw("request size validation failed with user error, sending as error input to consensus",
			"err", reqSizeErr,
			"requestSize", requestSize)

		input = &sdk.SimpleConsensusInputs{
			Observation: &sdk.SimpleConsensusInputs_Error{
				Error: reqSizeErr.Error(),
			},
			Descriptors: &sdk.ConsensusDescriptor{
				Descriptor_: &sdk.ConsensusDescriptor_Aggregation{
					Aggregation: sdk.AggregationType_AGGREGATION_TYPE_IDENTICAL,
				},
			},
			Default: nil,
		}
	}

	c.metrics.RecordRequestSize(ctx, float64(requestSize))

	callbackChan := c.sendRequest(ctx, input, consensusRequestMetaData)

	select {
	case <-ctx.Done():
		return nil, caperrors.NewPublicSystemError(ctx.Err(), caperrors.Canceled)
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
		return &responseAndMetadata, nil
	}
}

func (c *consensusCapability) sendRequest(ctx context.Context, input *sdk.SimpleConsensusInputs, consensusRequestMetaData oracle.ConsensusRequestMetadata) <-chan oracle.ConsensusResponse {
	c.requestTimeoutLock.RLock()
	requestTimeout := c.requestTimeout
	c.requestTimeoutLock.RUnlock()

	callbackChan := make(chan oracle.ConsensusResponse, 1)

	err := c.stricterMedianQuorum.Check(ctx, config.NewTimestamp(consensusRequestMetaData.ExecutionTimestamp))
	stricterMedianQuorum := err == nil
	if _, outOfRange := errors.AsType[limits.ErrorRangeLimited[config.Timestamp]](err); err != nil && !outOfRange {
		c.lggr.Warnw("error evaluating stricterMedianQuorum, defaulting to off", "error", err)
	}
	c.metrics.IncStricterMedianQuorum(ctx, "request", stricterMedianQuorum)

	err = c.includeAllTimestamps.Check(ctx, config.NewTimestamp(consensusRequestMetaData.ExecutionTimestamp))
	includeAllTimestamps := err == nil
	if _, outOfRange := errors.AsType[limits.ErrorRangeLimited[config.Timestamp]](err); err != nil && !outOfRange {
		c.lggr.Warnw("error evaluating includeAllTimestamps, defaulting to off", "error", err)
	}
	c.metrics.IncIncludeAllTimestamps(ctx, "request", includeAllTimestamps)

	req := oracle.NewConsensusRequest(input, time.Now(), time.Now().Add(requestTimeout), callbackChan,
		consensusRequestMetaData, c.observationQuorumTracker, stricterMedianQuorum, includeAllTimestamps,
	)

	c.reqHandler.SendRequest(ctx, req)
	return callbackChan
}

func validateReportRequest(reportRequest *sdk.ReportRequest) (string, error) {
	supportedKeyBundleIDs := map[string]struct{}{
		KeyBundleIDEvm:     {},
		KeyBundleIDAptos:   {},
		KeyBundleIDSolana:  {},
		KeyBundleIDStellar: {},
	}

	keyBundleID := strings.ToLower(reportRequest.EncoderName)
	if _, ok := supportedKeyBundleIDs[keyBundleID]; !ok {
		return "", fmt.Errorf("unsupported encoder name '%s' for report request. Supported encoder names are: %v", reportRequest.EncoderName, maps.Keys(supportedKeyBundleIDs))
	}

	signingAlgo := strings.ToLower(reportRequest.SigningAlgo)
	supportedSigningAlgorithms := map[string]struct{}{
		SigningAlgoEcdsa:   {},
		SigningAlgoEd25519: {},
	}

	if _, ok := supportedSigningAlgorithms[signingAlgo]; !ok {
		return "", fmt.Errorf("unsupported signing algorithm '%s' for report request. Supported signing algorithms are: %v", reportRequest.SigningAlgo, maps.Keys(supportedSigningAlgorithms))
	}

	hashingAlgo := strings.ToLower(reportRequest.HashingAlgo)

	supportedHashingAlgorithms := map[string]struct{}{
		HashingAlgoKeccak256:  {},
		HashingAlgoBlake2b256: {},
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

func (c *consensusCapability) Close() error {
	err := c.reqHandler.Close()
	if err != nil {
		c.lggr.Errorw("error closing request handler", "err", err)
	}

	if err := c.stricterMedianQuorum.Close(); err != nil {
		c.lggr.Errorw("error closing stricter median quorum limiter", "err", err)
	}

	if err := c.includeAllTimestamps.Close(); err != nil {
		c.lggr.Errorw("error closing include all timestamps limiter", "err", err)
	}

	if c.oracle != nil {
		if err := c.oracle.Close(); err != nil {
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
func (c *consensusCapability) validateRequestSize(ctx context.Context, lggr logger.Logger, consensusRequestMetaData oracle.ConsensusRequestMetadata, input proto.Message) (int, caperrors.Error) {
	lggr.Debugw("starting request size validation",
		"requestID", consensusRequestMetaData.RequestID(),
		"workflowID", consensusRequestMetaData.WorkflowID)

	requestMetaData := plugin.ToRequestMetaData(consensusRequestMetaData)

	serialisedInput, err := proto.Marshal(input)
	if err != nil {
		return 0, caperrors.NewPublicSystemError(fmt.Errorf("failed to serialise input: %w", err), caperrors.Internal)
	}
	inputSize := len(serialisedInput)

	serialisedMetadata, err := proto.Marshal(requestMetaData)
	if err != nil {
		return 0, caperrors.NewPublicSystemError(fmt.Errorf("failed to serialise metadata: %w", err), caperrors.Internal)
	}
	metadataSize := len(serialisedMetadata)

	totalSize := config.Size(inputSize + metadataSize)
	lggr.Debugw("calculated total size",
		"inputSizeBytes", inputSize,
		"metadataSizeBytes", metadataSize,
		"totalSizeBytes", int(totalSize))

	// Get the limit to log it
	limit, err := c.maxRequestSizeBytes.Limit(ctx)
	if err != nil {
		lggr.Warnw("failed to get limit, proceeding with check", "err", err)
	} else {
		lggr.Debugw("retrieved limit", "limitBytes", int(limit))
	}

	err = c.maxRequestSizeBytes.Check(ctx, totalSize)
	if err != nil {
		if limitErr, ok := errors.AsType[limits.ErrorBoundLimited[config.Size]](err); ok {
			lggr.Warnw("request size exceeds limit",
				"totalSizeBytes", int(totalSize),
				"limitBytes", int(limitErr.Limit),
				"excessBytes", int(totalSize-limitErr.Limit),
				"inputSizeBytes", inputSize,
				"metadataSizeBytes", metadataSize,
				"err", err)
			return int(totalSize), caperrors.NewLimitExceededError(fmt.Sprintf("request size %d bytes exceeds maximum allowed size", totalSize), err)
		}
		return int(totalSize), caperrors.NewPublicSystemError(fmt.Errorf("unexpected error checking request size limit: %w", err), caperrors.Internal)
	}

	lggr.Debugw("request size validation passed",
		"totalSizeBytes", int(totalSize),
		"limitBytes", int(limit))
	return int(totalSize), nil
}

func decodeObservationType(lggr logger.Logger, input *sdk.SimpleConsensusInputs) error {
	switch obs := input.GetObservation().(type) {
	case *sdk.SimpleConsensusInputs_Value:
		_, err := values.FromProto(obs.Value)
		if err != nil {
			lggr.Debugw("failed to decode observation value", "err", err)
			return fmt.Errorf("failed to decode observation value: %w", err)
		}
		lggr.Debugw("received observation value")
	case *sdk.SimpleConsensusInputs_Error:
		lggr.Debugw("observation is an error")
	default:
		if input.Default != nil {
			val, err := values.FromProto(input.Default)
			if err != nil {
				lggr.Debugw("failed to decode default observation value", "err", err)
				return fmt.Errorf("failed to decode default observation value: %w", err)
			}
			lggr.Debugw("serialised and using default value", "default_value", val)
		} else {
			lggr.Debugw("neither value, error or default is set in the observation input for request")
		}
	}
	return nil
}

func validateSimpleConsensusInput(input *sdk.SimpleConsensusInputs) error {
	if valueInput, ok := input.GetObservation().(*sdk.SimpleConsensusInputs_Value); ok {
		if err := oracle.ValidateAggregatableValue(valueInput.Value); err != nil {
			return fmt.Errorf("invalid observation value: %w", err)
		}
	}
	if err := oracle.ValidateAggregatableValue(input.Default); err != nil {
		return fmt.Errorf("invalid default value: %w", err)
	}

	return nil
}
