package actions

import (
	"context"
	"fmt"
	"sync"

	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/smartcontractkit/capabilities/readcontract/readcontractcap"
)

type ReadContractConfig struct {
	ChainID           uint64 `json:"chainId"`
	Network           string `json:"network"`
	SupportsConsensus bool   `json:"supportsConsensus"`
}

type Output struct {
	LatestValue values.Value `json:"latestValue"`
}

type ReadContractAction struct {
	services.StateMachine

	lggr logger.Logger
	capabilities.CapabilityInfo
	capabilities.Validator[readcontractcap.Config, readcontractcap.Input, capabilities.CapabilityResponse]

	relayer Relayer

	readContractStore *readContractStore

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

	return &ReadContractAction{
		lggr:              logger.Named(lggr, id),
		CapabilityInfo:    info,
		Validator:         capabilities.NewValidator[readcontractcap.Config, readcontractcap.Input, capabilities.CapabilityResponse](capabilities.ValidatorArgs{Info: info}),
		relayer:           relayer,
		clock:             clock,
		readContractStore: NewReadContractStore(),
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

	inputs, err := r.ValidateInputs(request.Inputs)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("invalid inputs: %w", err)
	}

	confidenceLevel, err := primitives.ConfidenceLevelFromString(inputs.ConfidenceLevel)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("invalid confidence level: %w", err)
	}

	reader, exists := r.readContractStore.Get(request.Metadata)
	if !exists {
		return capabilities.CapabilityResponse{}, fmt.Errorf("no contract reader found for workflow %s", request.Metadata.WorkflowID)
	}

	lggr.Info("Executing Get Latest Value request", "confidenceLevel", confidenceLevel, "params", inputs.Params)

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

func (r *ReadContractAction) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	config, err := r.ValidateConfig(request.Config)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	reader, err := r.relayer.NewContractReader(ctx, []byte(config.ContractReaderConfig))
	if err != nil {
		return fmt.Errorf("error fetching contract reader: %w", err)
	}

	if err = reader.Bind(ctx, []types.BoundContract{{Address: config.ContractAddress, Name: config.ContractName}}); err != nil {
		return fmt.Errorf("error binding read identifier: %w", err)
	}

	cr := &nonConsensusContractReader{contractReader: reader, readIdentifier: config.ReadIdentifier}

	r.readContractStore.Add(request.Metadata, cr)

	return nil
}

func (r *ReadContractAction) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	r.readContractStore.Remove(request.Metadata)
	return nil
}

type nonConsensusContractReader struct {
	contractReader ContractReader
	readIdentifier string
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
	GetLatestValue(ctx context.Context, requestID string,
		confidenceLevel primitives.ConfidenceLevel, params any) (values.Value, error)
}

type contractStoreKey struct {
	workflowID    string
	stepReference string
}

type readContractStore struct {
	mux   sync.Mutex
	store map[contractStoreKey]CapabilityContractReader
}

func NewReadContractStore() *readContractStore {
	return &readContractStore{store: make(map[contractStoreKey]CapabilityContractReader)}
}

func (r *readContractStore) Add(key capabilities.RegistrationMetadata, reader CapabilityContractReader) {
	r.mux.Lock()
	defer r.mux.Unlock()
	if r.store == nil {
		r.store = make(map[contractStoreKey]CapabilityContractReader)
	}
	r.store[contractStoreKey{
		workflowID:    key.WorkflowID,
		stepReference: key.ReferenceID,
	}] = reader
}

func (r *readContractStore) Remove(key capabilities.RegistrationMetadata) {
	r.mux.Lock()
	defer r.mux.Unlock()
	delete(r.store, contractStoreKey{
		workflowID:    key.WorkflowID,
		stepReference: key.ReferenceID,
	})
}

func (r *readContractStore) Get(key capabilities.RequestMetadata) (CapabilityContractReader, bool) {
	r.mux.Lock()
	defer r.mux.Unlock()

	storeKey := contractStoreKey{
		workflowID:    key.WorkflowID,
		stepReference: key.ReferenceID,
	}
	reader, exists := r.store[storeKey]
	return reader, exists
}
