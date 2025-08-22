package action

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/capabilities/decrypter/decryptercap"
)

var (
	ID         = "decrypter-action@1.0.0"
	actionInfo = capabilities.MustNewCapabilityInfo(
		ID,
		capabilities.CapabilityTypeAction,
		"Decrypts a message using the workflow key.",
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

func (ca *capability) Info(_ context.Context) (capabilities.CapabilityInfo, error) {
	return actionInfo, nil
}

func (ca *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	ca.lggr.Debugw("executing decrypter action")

	var input decryptercap.DecryptInputs
	if rawRequest.Inputs == nil || rawRequest.Inputs.Underlying == nil || rawRequest.Inputs.Underlying["DecryptInputs"] == nil {
		return capabilities.CapabilityResponse{}, errors.New("missing DecryptInputs in request")
	}
	if err := rawRequest.Inputs.Underlying["DecryptInputs"].UnwrapTo(&input); err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	accounts, err := ca.keystore.Accounts(ctx)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}
	if len(accounts) == 0 {
		return capabilities.CapabilityResponse{}, errors.New("no accounts found in keystore")
	}

	var acct string
	for _, a := range accounts {
		if a == core.StandardCapabilityAccount {
			acct = a
			break
		}
	}
	if acct == "" {
		return capabilities.CapabilityResponse{}, fmt.Errorf("no %s account found in keystore", core.StandardCapabilityAccount)
	}

	// The message is encrypted under each capability node's workflow key.
	// This loop trial decrypts until the node tries its corresponding ciphertext.
	var plaintext []byte
	var decryptErrors []error
	for _, ciphertext := range input.Ciphertexts {
		p, err := ca.keystore.Decrypt(ctx, acct, ciphertext)
		if err != nil {
			decryptErrors = append(decryptErrors, fmt.Errorf("failed to decrypt ciphertext %v: %w", ciphertext, err))
			continue
		}
		plaintext = p
		break
	}
	if len(plaintext) == 0 {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decrypt any ciphertexts: %w", errors.Join(decryptErrors...))
	}

	resp, err := values.WrapMap(decryptercap.DecryptOutputs{
		AccountID: acct,
		Plaintext: plaintext,
	})
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	return capabilities.CapabilityResponse{
		Value: resp,
	}, nil
}

func (ca *capability) RegisterToWorkflow(_ context.Context, rawRequest capabilities.RegisterToWorkflowRequest) error {
	ca.lggr.Debugw("registering to workflow", "workflowID", rawRequest.Metadata.WorkflowID, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
	return nil
}

func (ca *capability) UnregisterFromWorkflow(_ context.Context, rawRequest capabilities.UnregisterFromWorkflowRequest) error {
	ca.lggr.Debugw("unregistering from workflow", "workflowID", rawRequest.Metadata.WorkflowID, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
	return nil
}
