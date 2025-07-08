package action

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk"

	"github.com/smartcontractkit/capabilities/p2psigner/signercap"
)

var (
	ID         = "p2psigner-action@1.0.0"
	actionInfo = capabilities.MustNewCapabilityInfo(
		ID,
		capabilities.CapabilityTypeAction,
		"Signs a message using the P2P signing key.",
	)
)

type Params struct {
	Logger   logger.Logger
	Keystore core.Keystore
}

type Request struct {
	Metadata capabilities.RequestMetadata
	Config   *values.Map
	Inputs   sdk.CapMap
}

type capability struct {
	lggr     logger.Logger
	keystore core.Keystore
}

func New(p Params) (capabilities.ExecutableCapability, error) {
	return &capability{
		lggr:     p.Logger,
		keystore: p.Keystore,
	}, nil
}

func (c *capability) Info(_ context.Context) (capabilities.CapabilityInfo, error) {
	return actionInfo, nil
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.lggr.Debugw(
		"executing p2psigner action",
		"workflowID", rawRequest.Metadata.WorkflowID,
		"executionID", rawRequest.Metadata.WorkflowExecutionID,
		"workflowName", rawRequest.Metadata.WorkflowName,
		"workflowOwner", rawRequest.Metadata.WorkflowOwner,
	)

	var input signercap.SignInputs
	if rawRequest.Inputs == nil || rawRequest.Inputs.Underlying == nil || rawRequest.Inputs.Underlying["SignInputs"] == nil {
		return capabilities.CapabilityResponse{}, errors.New("missing SignInputs in request")
	}
	if err := rawRequest.Inputs.Underlying["SignInputs"].UnwrapTo(&input); err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	accounts, err := c.keystore.Accounts(ctx)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}
	if len(accounts) == 0 {
		return capabilities.CapabilityResponse{}, errors.New("no accounts found in keystore")
	}

	var acct string
	for _, a := range accounts {
		if a == core.P2PAccountKey {
			acct = a
			break
		}
	}
	if acct == "" {
		return capabilities.CapabilityResponse{}, fmt.Errorf("no %s account found in keystore", core.P2PAccountKey)
	}

	sig, err := c.keystore.Sign(ctx, acct, input.Digest)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	resp, err := values.WrapMap(signercap.SignOutputs{
		AccountID: acct,
		Signature: sig,
	})
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	return capabilities.CapabilityResponse{
		Value: resp,
	}, nil
}

func (c *capability) RegisterToWorkflow(_ context.Context, rawRequest capabilities.RegisterToWorkflowRequest) error {
	c.lggr.Debugw("registering to workflow", "workflowID", rawRequest.Metadata.WorkflowID, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
	return nil
}

func (c *capability) UnregisterFromWorkflow(_ context.Context, rawRequest capabilities.UnregisterFromWorkflowRequest) error {
	c.lggr.Debugw("unregistering from workflow", "workflowID", rawRequest.Metadata.WorkflowID, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
	return nil
}
