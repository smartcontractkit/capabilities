package actions

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	readcontractcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/actions/readcontract"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

const (
	defaultCacheCleanupInterval          = 1 * time.Minute
	defaultCacheExpiryTime               = 1 * time.Hour
	defaultCacheSizeBeforeCleanupEnacted = 100

	defaultMaximumBindTime = 30 * time.Second
)

type ReadContractConfig struct {
	ChainID           uint64 `json:"chainId"`
	Network           string `json:"network"`
	SupportsConsensus bool   `json:"supportsConsensus"`
}

type Output struct {
	LatestValue values.Value `json:"latestValue"`
}

var (
	readContractCacheHit = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "readcontract_capability_cache_hit",
		Help: "hit vs non-hits of the read contract capability cache",
	}, []string{"hit"})
	readContractCacheEviction = promauto.NewCounter(prometheus.CounterOpts{
		Name: "readcontract_capability_cache_eviction",
		Help: "evictions from the read contract cache",
	})
	readContractCacheAddition = promauto.NewCounter(prometheus.CounterOpts{
		Name: "readcontract_capability_cache_addition",
		Help: "additions to the read contract cache",
	})
)

type readContractCacheStats struct {
}

func (r readContractCacheStats) OnCacheHit() {
	readContractCacheHit.WithLabelValues("true").Inc()
}

func (r readContractCacheStats) OnCacheMiss() {
	readContractCacheHit.WithLabelValues("false").Inc()
}

func (r readContractCacheStats) OnCacheEviction(i int) {
	readContractCacheEviction.Add(float64(i))
}

func (r readContractCacheStats) OnCacheAddition() {
	readContractCacheAddition.Inc()
}

type ReadContractAction struct {
	services.StateMachine

	lggr logger.Logger
	capabilities.CapabilityInfo
	capabilities.Validator[readcontractcap.Config, readcontractcap.Input, capabilities.CapabilityResponse]

	relayer Relayer

	contractReaders *ServiceCache[string, CapabilityContractReader]

	mux   sync.Mutex
	clock clockwork.Clock
}

type Relayer interface {
	NewContractReader(_ context.Context, contractReaderConfig []byte) (ContractReader, error)
}

type ContractReader interface {
	services.Service
	GetLatestValueWithHeadData(ctx context.Context, readIdentifier string, confidenceLevel primitives.ConfidenceLevel, params, returnVal any) (*types.Head, error)
	Bind(ctx context.Context, bindings []types.BoundContract) error
}

func NewReadContractAction(_ context.Context, lggr logger.Logger, config ReadContractConfig, relayer Relayer,
	_ core.OracleFactory, clock clockwork.Clock) (*ReadContractAction, error) {
	id := fmt.Sprintf("read-contract-%s-%d@1.0.0", config.Network, config.ChainID)

	info, err := capabilities.NewCapabilityInfo(
		id,
		capabilities.CapabilityTypeAction,
		"Read Contract Action.  Supports reading from a contract.",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create capability info: %w", err)
	}

	contractReaderCache := NewServiceCache[string, CapabilityContractReader](lggr, "ContractReaderCache",
		clockwork.NewRealClock(), defaultCacheCleanupInterval, defaultCacheExpiryTime, defaultCacheSizeBeforeCleanupEnacted, readContractCacheStats{})

	return &ReadContractAction{
		lggr:            logger.Named(lggr, id),
		CapabilityInfo:  info,
		Validator:       capabilities.NewValidator[readcontractcap.Config, readcontractcap.Input, capabilities.CapabilityResponse](capabilities.ValidatorArgs{Info: info}),
		relayer:         relayer,
		clock:           clock,
		contractReaders: contractReaderCache,
	}, nil
}

func (r *ReadContractAction) Start(ctx context.Context) error {
	return nil
}

func (r *ReadContractAction) Close() error {
	return nil
}

func (r *ReadContractAction) Execute(ctx context.Context, request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	lggr := logger.With(r.lggr, "workflow", request.Metadata)

	config, err := r.ValidateConfig(request.Config)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("invalid config: %w", err)
	}

	inputs, err := r.ValidateInputs(request.Inputs)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("invalid inputs: %w", err)
	}

	confidenceLevel, err := primitives.ConfidenceLevelFromString(inputs.ConfidenceLevel)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("invalid confidence level: %w", err)
	}

	reader, err := r.getContractReader(ctx, config.ContractReaderConfig, config.ReadIdentifier, request.Metadata.WorkflowID)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to get contract reader: %w", err)
	}

	// If the initial loopp connection to the contract reader fails due to incorrect binding (or any other cause) the
	// bind method will block retrying to establish the connection and never succeeding until the ctx expires, this is
	// a workaround so that the bind method returns in a more timely fashion.
	ctxWithTimeout, cancel := context.WithTimeout(ctx, defaultMaximumBindTime)
	defer cancel()
	if err = reader.Bind(ctxWithTimeout, []types.BoundContract{{Address: config.ContractAddress, Name: config.ContractName}}); err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("error binding read identifier: %w", err)
	}

	lggr.Infow("Executing Get Latest Value request", "readIdentifier", config.ReadIdentifier, "address", config.ContractAddress,
		"confidenceLevel", confidenceLevel, "params", inputs.Params)

	resp, err := reader.GetLatestValue(ctx, request.Metadata.WorkflowExecutionID, confidenceLevel, inputs.Params)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to get latest value: %w", err)
	}

	output := Output{LatestValue: resp}
	resultValue, err := values.WrapMap(&output)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("error wrapping output: %w", err)
	}

	return capabilities.CapabilityResponse{Value: resultValue}, nil
}

func (r *ReadContractAction) getContractReader(ctx context.Context, contractReaderConfig string, readIdentifier string,
	workflowID string) (CapabilityContractReader, error) {
	r.mux.Lock()
	defer r.mux.Unlock()

	contractReaderConfigID := fmt.Sprintf("%x", sha256.Sum256([]byte(contractReaderConfig+readIdentifier+workflowID)))
	if reader, ok := r.contractReaders.Get(contractReaderConfigID); ok {
		return reader, nil
	}

	reader, err := r.relayer.NewContractReader(ctx, []byte(contractReaderConfig))
	if err != nil {
		return nil, fmt.Errorf("error fetching contract reader: %w", err)
	}

	capabiltyContractReader := &nonConsensusContractReader{
		contractReader: reader,
		readIdentifier: readIdentifier,
	}

	err = r.contractReaders.AddAndStart(ctx, contractReaderConfigID, capabiltyContractReader)
	if err != nil {
		return nil, fmt.Errorf("error adding contract reader to cache: %w", err)
	}
	return capabiltyContractReader, nil
}

func (r *ReadContractAction) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	// Do Nothing - currently expected that this method is to be removed from executable capabilities
	return nil
}

func (r *ReadContractAction) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	// Do Nothing - currently expected that this method is to be removed from executable capabilities
	return nil
}

type nonConsensusContractReader struct {
	contractReader ContractReader
	readIdentifier string
}

func (n *nonConsensusContractReader) Start(ctx context.Context) error {
	return n.contractReader.Start(ctx)
}

func (n *nonConsensusContractReader) Close() error {
	return n.contractReader.Close()
}

func (n *nonConsensusContractReader) Bind(ctx context.Context, bindings []types.BoundContract) error {
	return n.contractReader.Bind(ctx, bindings)
}

func (n *nonConsensusContractReader) Ready() error {
	return n.contractReader.Ready()
}

func (n *nonConsensusContractReader) HealthReport() map[string]error {
	return n.contractReader.HealthReport()
}

func (n *nonConsensusContractReader) Name() string {
	return n.contractReader.Name()
}

func (n *nonConsensusContractReader) GetLatestValue(ctx context.Context, requestID string,
	confidenceLevel primitives.ConfidenceLevel, params any) (values.Value, error) {
	var value values.Value
	_, err := n.contractReader.GetLatestValueWithHeadData(ctx, n.readIdentifier, confidenceLevel, params, &value)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest value fron contract reader: %w", err)
	}
	return value, nil
}

type CapabilityContractReader interface {
	services.Service
	GetLatestValue(ctx context.Context, requestID string,
		confidenceLevel primitives.ConfidenceLevel, params any) (values.Value, error)
	Bind(ctx context.Context, bindings []types.BoundContract) error
}
