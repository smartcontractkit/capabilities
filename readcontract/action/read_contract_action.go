package actions

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/smartcontractkit/capabilities/readcontract/readcontractcap"
)

const (
	defaultCacheCleanupInterval          = 1 * time.Minute
	defaultCacheExpiryTime               = 1 * time.Hour
	defaultCacheSizeBeforeCleanupEnacted = 100
)

type ReadContractConfig struct {
	ChainID uint64 `json:"chainId"`
	Network string `json:"network"`
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
	lggr logger.Logger

	capabilities.CapabilityInfo
	capabilities.Validator[readcontractcap.Config, readcontractcap.Input, capabilities.CapabilityResponse]

	relayer Relayer

	mux             sync.Mutex
	contractReaders *ServiceCache[string, ContractReader]
}

type Relayer interface {
	NewContractReader(_ context.Context, contractReaderConfig []byte) (ContractReader, error)
}

type ContractReader interface {
	services.Service
	GetLatestValue(ctx context.Context, readIdentifier string, confidenceLevel primitives.ConfidenceLevel, params, returnVal any) error
	Bind(ctx context.Context, bindings []types.BoundContract) error
}

func NewReadContractAction(lggr logger.Logger, config ReadContractConfig, relayer Relayer) (*ReadContractAction, error) {
	id := fmt.Sprintf("read-contract-%s-%d@0.1.0", config.Network, config.ChainID)

	info, err := capabilities.NewCapabilityInfo(
		id,
		capabilities.CapabilityTypeAction,
		"Read Contract Action.  Supports reading from a contract.",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create capability info: %w", err)
	}

	contractReaderCache := NewServiceCache[string, ContractReader](lggr, "ContractReaderCache",
		clockwork.NewRealClock(), defaultCacheCleanupInterval, defaultCacheExpiryTime, defaultCacheSizeBeforeCleanupEnacted, readContractCacheStats{})

	return &ReadContractAction{
		lggr:            logger.Named(lggr, id),
		CapabilityInfo:  info,
		Validator:       capabilities.NewValidator[readcontractcap.Config, readcontractcap.Input, capabilities.CapabilityResponse](capabilities.ValidatorArgs{Info: info}),
		relayer:         relayer,
		contractReaders: contractReaderCache,
	}, nil
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

	reader, err := r.getContractReader(ctx, config.ContractReaderConfig)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to get contract reader: %w", err)
	}

	if err = reader.Bind(ctx, []types.BoundContract{{Address: inputs.Address, Name: inputs.ReadIdentifier}}); err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("error binding read identifier: %w", err)
	}

	lggr.Info("Executing Get Latest Value request", "readIdentifier", inputs.ReadIdentifier, "address", inputs.Address,
		"confidenceLevel", confidenceLevel, "params", inputs.Params)

	var result values.Value
	if err = reader.GetLatestValue(ctx, inputs.ReadIdentifier, confidenceLevel, inputs.Params, &result); err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("error getting latest service: %w", err)
	}

	output := Output{LatestValue: result}
	resultValue, err := values.WrapMap(&output)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("error wrapping output: %w", err)
	}

	return capabilities.CapabilityResponse{Value: resultValue}, nil
}

func (r *ReadContractAction) getContractReader(ctx context.Context, contractReaderConfig string) (ContractReader, error) {
	r.mux.Lock()
	defer r.mux.Unlock()

	contractReaderConfigID := fmt.Sprintf("%x", sha256.Sum256([]byte(contractReaderConfig)))
	if reader, ok := r.contractReaders.Get(contractReaderConfigID); ok {
		return reader, nil
	}

	jsonBytes, err := json.Marshal(contractReaderConfig)
	if err != nil {
		return nil, fmt.Errorf("error marshaling contract reader config: %w", err)
	}

	reader, err := r.relayer.NewContractReader(ctx, jsonBytes)
	if err != nil {
		return nil, fmt.Errorf("error fetching contract reader: %w", err)
	}

	err = r.contractReaders.AddAndStart(ctx, contractReaderConfigID, reader)
	if err != nil {
		return nil, fmt.Errorf("error adding contract reader to cache: %w", err)
	}
	return reader, nil
}

func (r *ReadContractAction) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	// Do Nothing - there are no resources managed by this capability that match the lifecycle of a workflow
	return nil
}

func (r *ReadContractAction) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	// Do Nothing - there are no resources managed by this capability that match the lifecycle of a workflow
	return nil
}
