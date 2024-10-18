package action

import (
	"context"
	"crypto/ecdsa"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/smartcontractkit/capabilities/sign/signcap"
)

var _ capabilities.ActionCapability = (*capability)(nil)

type capability struct {
	logger logger.Logger
	store  core.KeyValueStore
}

type Params struct {
	Logger logger.Logger
	Store  core.KeyValueStore
}

func New(p Params) *capability {
	return &capability{
		logger: p.Logger,
		store:  p.Store,
	}
}

func (c *capability) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo("sign-action@1.0.0", capabilities.CapabilityTypeAction, "Sign data")
}

type Request struct {
	Metadata capabilities.RequestMetadata
	Inputs   signcap.Inputs
}

func evaluate(rawRequest capabilities.CapabilityRequest) (r Request, err error) {
	r.Metadata = rawRequest.Metadata

	if rawRequest.Inputs == nil {
		return r, fmt.Errorf("missing inputs field")
	}

	data, ok := rawRequest.Inputs.Underlying["data"]
	if !ok {
		return r, fmt.Errorf("missing required field %s", data)
	}

	if err = data.UnwrapTo(&r.Inputs.Data); err != nil {
		return r, fmt.Errorf("failed to unwrap signed report: %v", err)
	}

	return r, nil
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.logger.Debug("Executing", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)

	request, err := evaluate(rawRequest)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode request: %v", err)
	}

	var config signcap.ActionConfig
	err = rawRequest.Config.UnwrapTo(&config)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to get config: %v", err)
	}
	c.logger.Debug("Values stored", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID", rawRequest.Metadata.WorkflowExecutionID)

	privateKey, err := GetPrivateKeyFromString(config.PrivateKey)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to get pkey: %v", err)
	}

	sig, err := crypto.Sign(request.Inputs.Data, privateKey)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to sign: %v", err)
	}

	c.logger.Infof("Signed message: %s, %s, %s", common.Bytes2Hex(request.Inputs.Data), common.Bytes2Hex(sig), config.PrivateKey)

	outputs := signcap.Outputs{
		Sig: sig,
	}

	valsMap, err := values.WrapMap(outputs)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	return capabilities.CapabilityResponse{
		Value: valsMap,
	}, nil
}

func (c *capability) RegisterToWorkflow(ctx context.Context, rawRequest capabilities.RegisterToWorkflowRequest) error {
	c.logger.Debug("Registering to workflow", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID")

	return nil
}

func (c *capability) UnregisterFromWorkflow(ctx context.Context, rawRequest capabilities.UnregisterFromWorkflowRequest) error {
	c.logger.Debug("Unregistering from workflow", "WorkflowID", rawRequest.Metadata.WorkflowID, "WorkflowExecutionID")
	return nil
}

func GetPrivateKeyFromString(privateKeyStr string) (*ecdsa.PrivateKey, error) {
	if len(privateKeyStr) > 2 && privateKeyStr[:2] == "0x" {
		privateKeyStr = privateKeyStr[2:]
	}

	privateKeyBytes, err := hexutil.Decode("0x" + privateKeyStr)
	if err != nil {
		return nil, fmt.Errorf("failed to decode private key: %v", err)
	}

	privateKey, err := crypto.ToECDSA(privateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to ECDSA: %v", err)
	}

	return privateKey, nil
}
