package actions

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

type ReadContractConfig struct {
	ChainId uint64 `json:"chainId"`
	Network string `json:"network"`
}

type RequestConfig struct {
	ContractReaderConfig string `json:"contractReaderConfig"`
}

type Input struct {
	ReadIdentifier  string         `json:"readIdentifier"`
	Address         string         `json:"address"`
	ConfidenceLevel string         `json:"confidenceLevel"`
	Params          map[string]any `json:"params"`
}

const LatestValue = "latestValue"

type ReadContractAction struct {
	lggr logger.Logger

	capabilities.CapabilityInfo
	capabilities.Validator[RequestConfig, Input, capabilities.CapabilityResponse]

	relayer Relayer

	mux             sync.Mutex
	contractReaders map[string]ContractReader
}

type Relayer interface {
	NewContractReader(_ context.Context, contractReaderConfig []byte) (ContractReader, error)
}

type ContractReader interface {
	GetLatestValue(ctx context.Context, readIdentifier string, confidenceLevel primitives.ConfidenceLevel, params, returnVal any) error
	Bind(ctx context.Context, bindings []types.BoundContract) error
}

func NewReadContractAction(lggr logger.Logger, config ReadContractConfig, relayer Relayer) *ReadContractAction {
	id := fmt.Sprintf("read-contract-%s-%d@1.0.0", config.Network, config.ChainId)

	info := capabilities.MustNewCapabilityInfo(
		id,
		capabilities.CapabilityTypeAction,
		"Read Contract Action.  Supports reading from a contract.",
	)

	return &ReadContractAction{
		lggr:            lggr,
		CapabilityInfo:  info,
		Validator:       capabilities.NewValidator[RequestConfig, Input, capabilities.CapabilityResponse](capabilities.ValidatorArgs{Info: info}),
		relayer:         relayer,
		contractReaders: map[string]ContractReader{},
	}
}

func (r *ReadContractAction) Execute(ctx context.Context, request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {

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

	var result values.Value
	if err = reader.GetLatestValue(ctx, inputs.ReadIdentifier, confidenceLevel, inputs.Params, &result); err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("error getting latest value: %w", err)
	}

	resultMap := map[string]any{}
	resultMap[LatestValue] = result
	valuesMap, err := values.NewMap(resultMap)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("error creating result map: %w", err)
	}

	return capabilities.CapabilityResponse{Value: valuesMap}, nil
}

func (r *ReadContractAction) getContractReader(ctx context.Context, contractReaderConfig string) (ContractReader, error) {
	r.mux.Lock()
	defer r.mux.Unlock()

	contractReaderConfigID := fmt.Sprintf("%x", sha256.Sum256([]byte(contractReaderConfig)))
	if reader, ok := r.contractReaders[contractReaderConfigID]; ok {
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

	r.contractReaders[contractReaderConfigID] = reader
	return reader, nil
}

func (r *ReadContractAction) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	// Do Nothing
	return nil
}

func (r *ReadContractAction) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	// Do Nothing
	return nil
}
