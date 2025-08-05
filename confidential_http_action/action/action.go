package action

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"google.golang.org/protobuf/types/known/anypb"

	cap "github.com/smartcontractkit/capabilities/confidential_http_action/confidential_http_action_cap"
	enclaveclient "github.com/smartcontractkit/confidential-compute/enclave-client"
	httpenclavetypes "github.com/smartcontractkit/confidential-compute/enclave/nitro-confidential-http-enclave/types"
	enclavetypes "github.com/smartcontractkit/confidential-compute/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/actions/vault"
)

var (
	ID                         = "confidential-http-action@1.0.0"
	confidentialHttpActionInfo = capabilities.MustNewCapabilityInfo(
		ID,
		capabilities.CapabilityTypeAction,
		"Executes an HTTP request confidentially, by perhaps using secrets from the VaultDON",
	)
)

func (c *capability) Info(_ context.Context) (capabilities.CapabilityInfo, error) {
	return confidentialHttpActionInfo, nil
}

type capability struct {
	lggr                    logger.Logger
	enclaveClient           enclaveclient.EnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse]
	keystore                core.Keystore
	vaultDONMasterPublicKey []byte
	vaultDONID              []byte
	vaultDONCapability      capabilities.ExecutableCapability
}

// parseEnclaveType converts a string into an EnclaveType using case-insensitive matching.
// It handles the case where the source type is a pointer and might be nil. If nil, returns error.
func parseEnclaveType(typeStr *string) (*enclavetypes.EnclaveType, error) {
	if typeStr == nil {
		return nil, errors.New("enclave type cannot be nil")
	}

	// Convert input to upper case for case-insensitive matching.
	upperType := strings.ToUpper(*typeStr)
	switch enclavetypes.EnclaveType(upperType) {
	case enclavetypes.EnclaveTypeNitro, enclavetypes.EnclaveTypeSGX, enclavetypes.EnclaveTypeTDX, enclavetypes.EnclaveTypeSEV:
		et := enclavetypes.EnclaveType(upperType)
		return &et, nil
	default:
		return nil, errors.New("invalid enclave type: " + *typeStr)
	}
}

// GetNodes transforms a slice of Enclave structs from the config into a slice of EnclaveNode structs.
func GetNodes(config cap.Config) ([]enclavetypes.EnclaveNode, error) {
	// Pre-allocate the slice with the correct capacity for efficiency.
	nodes := make([]enclavetypes.EnclaveNode, 0, len(config.Enclaves))

	for i, confEnclave := range config.Enclaves {
		// The EnclaveID in EnclaveNode is a fixed-size [32]byte array.
		// The ID in the source Enclave is a []byte slice.
		// We must copy the data from the slice to the array.
		var enclaveID [32]byte
		if len(confEnclave.ID) > 32 {
			// Returning an error on data loss is safer than silent truncation.
			return nil, fmt.Errorf("enclave at index %d has an ID longer than 32 bytes", i)
		}
		copy(enclaveID[:], confEnclave.ID)

		enclaveType, err := parseEnclaveType(confEnclave.EnclaveType)
		if err != nil {
			return nil, fmt.Errorf("failed to parse enclave type for enclave at index %d: %w", i, err)
		}
		node := enclavetypes.EnclaveNode{
			EnclaveURL:       confEnclave.URL,
			TrustedValues:    confEnclave.TrustedValues, // Directly compatible ( []uint8 is alias for []byte )
			EnclaveExtraData: confEnclave.ExtraData,     // Directly compatible
			EnclaveType:      *enclaveType,
			EnclaveID:        enclaveID,
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

func New(
	lggr logger.Logger,
	capConfig cap.Config,
	keystore core.Keystore,
	vaultDONCapability capabilities.ExecutableCapability,
	vaultDONMasterPublicKey []byte,
) (*capability, error) {
	nodes, err := GetNodes(capConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create enclave pool: %w", err)
	}

	pool, err := enclaveclient.NewPool[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse](
		nodes, vaultDONMasterPublicKey, nil, nil, nil, nil, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create enclave pool: %w", err)
	}

	// Now delegate to NewWithEnclaveClient, which will NOT call getvaultDONMasterPublicKey again.
	return NewWithEnclaveClient(lggr, capConfig, keystore, pool, vaultDONMasterPublicKey, vaultDONCapability)
}

// NewWithEnclaveClient allows injecting a custom (e.g., mock) EnclaveClient for testing.
// Accepts vaultDONMasterPublicKey as a parameter to avoid duplicate lookups.
func NewWithEnclaveClient(
	lggr logger.Logger,
	capConfig cap.Config,
	keystore core.Keystore,
	enclaveClient enclaveclient.EnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse],
	vaultDONMasterPublicKey []byte,
	vaultDONCapability capabilities.ExecutableCapability,
) (*capability, error) {
	return &capability{
		lggr:                    lggr,
		enclaveClient:           enclaveClient,
		keystore:                keystore,
		vaultDONMasterPublicKey: vaultDONMasterPublicKey,
		vaultDONID:              capConfig.VaultDONID,
		vaultDONCapability:      vaultDONCapability,
	}, nil
}

func sanitizeLogString(s string) string {
	// Remove newlines to prevent log injection/splitting
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) > 256 {
		s = s[:256] + "..."
	}
	return s
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.lggr.Debugw("executing", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner)

	// Parse user input.
	if rawRequest.Inputs == nil {
		return capabilities.CapabilityResponse{}, errors.New("missing inputs field")
	}

	reqID := sha256.Sum256([]byte(rawRequest.Metadata.WorkflowExecutionID))

	// Fetch enclave params that can be used for this request.
	enclaveParams, err := c.getEnclaveParams(ctx, reqID)

	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	var input cap.Input
	err = rawRequest.Inputs.UnwrapTo(&input)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	publicData, err := ConvertInputToHTTPEnclaveRequestData(input)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	publicDataBytes, err := json.Marshal(publicData)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to marshal templates: %w", err)
	}

	encryptedSecrets, encryptedDecyrptedShares, err := c.GetEncryptedDecryptedShares(
		ctx,
		input.VaultDONSecrets,
		enclaveParams.EnclaveEphemeralPublicKey,
		rawRequest.Metadata.WorkflowOwner)

	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to get encrypted decryption key shares from VaultDON: %w", err)
	}

	computeReq := enclavetypes.ComputeRequest{
		RequestID:                    reqID,
		PublicData:                   publicDataBytes,
		Ciphertexts:                  encryptedSecrets,
		MasterPublicKey:              c.vaultDONMasterPublicKey,
		EnclaveEphemeralPublicKey:    enclaveParams.EnclaveEphemeralPublicKey,
		EncryptedDecryptionKeyShares: encryptedDecyrptedShares,
	}
	signedComputeReq, err := c.SignComputeRequest(ctx, computeReq)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to sign compute request: %w", err)
	}

	rawExecuteResponses, err := c.enclaveClient.ExecuteBatch(ctx, []enclavetypes.SignedComputeRequest{*signedComputeReq}, [][32]byte{enclaveParams.EnclaveID})
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to execute enclave request: %w", err)
	}

	if len(rawExecuteResponses) != 1 {
		return capabilities.CapabilityResponse{}, fmt.Errorf("expected one enclave response, got %d", len(rawExecuteResponses))
	}

	// As with the enclave request data, output must be converted from the enclave type to the SDK type.
	var responses []cap.OutputResponsesElem
	err = json.Unmarshal(rawExecuteResponses[0].Output, &responses)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode enclave response: %w", err)
	}

	var respBodies []string
	for _, r := range responses {
		respBodies = append(respBodies, string(r.Body))
	}
	c.lggr.Infow("confidentialhttpcap capability has validated an attested batch of HTTP responses",
		"workflowID", sanitizeLogString(rawRequest.Metadata.WorkflowID),
		"executionID", sanitizeLogString(rawRequest.Metadata.WorkflowExecutionID),
		"workflowName", sanitizeLogString(rawRequest.Metadata.WorkflowName),
		"responses", respBodies,
		"attestation", rawExecuteResponses[0].Attestation)

	// Return the response.
	valsMap, err := values.WrapMap(cap.Output{
		Responses: responses,
	})
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}
	return capabilities.CapabilityResponse{
		Value: valsMap,
	}, nil
}

func (c *capability) SignComputeRequest(ctx context.Context, computeRequest enclavetypes.ComputeRequest) (*enclavetypes.SignedComputeRequest, error) {
	accounts, err := c.keystore.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, errors.New("no accounts found in keystore")
	}

	var acct string
	for _, a := range accounts {
		if a == core.P2PAccountKey {
			acct = a
			break
		}
	}
	if acct == "" {
		return nil, fmt.Errorf("no %s account found in keystore", "capability-signing-key")
	}

	hash := computeRequest.Hash()

	sig, err := c.keystore.Sign(ctx, acct, hash[:])

	if err != nil {
		return nil, err
	}

	return &enclavetypes.SignedComputeRequest{
		ComputeRequest: computeRequest,
		Signature:      sig,
	}, nil
}

type EnclaveParams struct {
	EnclaveID                 [32]byte
	EnclaveEphemeralPublicKey []byte
}

func (c *capability) getEnclaveParams(ctx context.Context, reqID [32]byte) (*EnclaveParams, error) {
	ephemeralPubKeyResponse, err := c.enclaveClient.GetPublicKeys(ctx, reqID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get public keys: %w", err)
	}

	if len(ephemeralPubKeyResponse) == 0 || len(ephemeralPubKeyResponse[0].PublicKeys) == 0 {
		return nil, fmt.Errorf("no enclave public keys found for request %x", reqID)
	}

	// Out of the enclave responses returned, pick the first response (this capability only uses one enclave).
	// Then, select the most recently created public key from that enclave.
	// Note that during the enclaveClient construction, we can set the enclave node selector to select a specific enclave based on criteria
	// like the most recent public key, or any other such.
	selectedEnclaveResponse := ephemeralPubKeyResponse[0]
	var mostRecentPubKeyIndex, mostRecentPubKeyCreationTime int64
	for i, time := range selectedEnclaveResponse.CreationTimes {
		if time.UnixMicro() > mostRecentPubKeyCreationTime {
			mostRecentPubKeyIndex = int64(i)
			mostRecentPubKeyCreationTime = time.UnixMicro()
		}
	}
	selectedEphemeralPublicKey := selectedEnclaveResponse.PublicKeys[mostRecentPubKeyIndex]
	selectedEnclaveID := selectedEnclaveResponse.EnclaveID
	c.lggr.Info(fmt.Sprintf("using enclave public key: %x for request %x", selectedEphemeralPublicKey, reqID))
	return &EnclaveParams{
		EnclaveID:                 selectedEnclaveID,
		EnclaveEphemeralPublicKey: selectedEphemeralPublicKey,
	}, nil
}

func ConvertInputToHTTPEnclaveRequestData(input cap.Input) (*httpenclavetypes.HTTPEnclaveRequestData, error) {
	var convertedRequests []httpenclavetypes.RequestTemplate
	err := mapstructure.Decode(input.Requests, &convertedRequests)
	if err != nil {
		return nil, fmt.Errorf("failed to decode requests: %w", err)
	}

	inputCiphertextNames := make([]string, 0, len(input.VaultDONSecrets))
	for _, secret := range input.VaultDONSecrets {
		inputCiphertextNames = append(inputCiphertextNames, secret.Key)
	}

	return &httpenclavetypes.HTTPEnclaveRequestData{
		Requests:                convertedRequests,
		TemplateCiphertextNames: inputCiphertextNames,
	}, nil
}

type VaultDONInput struct {
	SecretIDs                 []string `json:"secretIds"`
	EnclaveEphemeralPublicKey []byte   `json:"enclaveEphemeralPublicKey"`
}

type VaultDONOutput struct {
	EncryptedSecrets             [][]byte   `json:"encryptedSecrets"`
	EncryptedDecryptionKeyShares [][][]byte `json:"encryptedDecryptionKeyShares"`
}

func (c *capability) GetEncryptedDecryptedShares(
	ctx context.Context,
	vaultDONSecrets []cap.SecretIdentifier,
	enclaveEphemeralPublicKey []byte,
	workflowOwner string,
) ([][]byte, [][][]byte, error) {
	c.lggr.Debugw("Attempting to get encrypted decrypted shares from VaultDON capability",
		"vaultDONID", fmt.Sprintf("%x", c.vaultDONID),
		"enclaveEphemeralPublicKey", fmt.Sprintf("%x", enclaveEphemeralPublicKey[:8])) // Log first 8 bytes for brevity

	if c.vaultDONCapability == nil {
		return nil, nil, errors.New("VaultDON capability is not initialized")
	}

	secretRequests := make([]*vault.SecretRequest, len(vaultDONSecrets))
	for i, secret := range vaultDONSecrets {
		secretRequests[i] = &vault.SecretRequest{
			Id: &vault.SecretIdentifier{
				Key:       secret.Key,
				Namespace: secret.Namespace,
				Owner:     workflowOwner,
			},
			// Set EncryptionKeys to be an array of 1 item with the enclaveEphemeralPublicKey
			EncryptionKeys: []string{string(enclaveEphemeralPublicKey)},
		}
		if secret.Owner != nil {
			secretRequests[i].Id.Owner = *secret.Owner
		}
	}

	vaultDONRequestPayload := &vault.GetSecretsRequest{
		Requests: secretRequests,
	}

	inputAny, err := anypb.New(vaultDONRequestPayload)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal VaultDON request payload to Any: %w", err)
	}

	// Create a CapabilityRequest for the VaultDON
	vaultDONRequest := capabilities.CapabilityRequest{
		Payload: inputAny, // Assign the *anypb.Any here
		Method:  vault.MethodGetSecrets,
		Metadata: capabilities.RequestMetadata{ // Corrected: Metadata is a struct value
			WorkflowID:          "confidential-http-action-request", // Example ID
			WorkflowExecutionID: "vault-don-secrets-fetch",          // Example ID
		},
	}

	// Execute the VaultDON capability
	vaultDONResponse, err := c.vaultDONCapability.Execute(ctx, vaultDONRequest)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute VaultDON capability: %w", err)
	}

	// Unwrap the response from the VaultDON using vault.GetSecretsResponse
	var vaultDONOutput vault.GetSecretsResponse
	err = vaultDONResponse.Payload.UnmarshalTo(&vaultDONOutput)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal VaultDON response payload: %w", err)
	}

	// Process the responses from the VaultDON
	encryptedSecrets := make([][]byte, 0)
	encryptedDecryptedShares := make([][][]byte, 0)

	for _, secretResp := range vaultDONOutput.Responses {
		if secretResp.GetError() != "" {
			c.lggr.Warnw("VaultDON returned an error for a secret", "secretID", secretResp.GetId().GetKey(), "error", secretResp.GetError())
			continue // Skip this secret if there was an error
		}

		secretData := secretResp.GetData()
		if secretData == nil {
			c.lggr.Warnw("VaultDON returned no data for a secret", "secretID", secretResp.GetId().GetKey())
			continue
		}

		encryptedSecrets = append(encryptedSecrets, []byte(secretData.GetEncryptedValue()))
		encryptedDecryptedSharesForSecret := make([][]byte, 0)

		if len(secretData.GetEncryptedDecryptionKeyShares()) != 1 {
			return nil, nil, fmt.Errorf("expected exactly one set of encrypted decryption key shares for secret %s, got %d", secretResp.GetId().GetKey(), len(secretData.GetEncryptedDecryptionKeyShares()))
		}
		for _, shareStr := range secretData.GetEncryptedDecryptionKeyShares()[0].GetShares() {
			encryptedDecryptedSharesForSecret = append(encryptedDecryptedSharesForSecret, []byte(shareStr))
		}
		encryptedDecryptedShares = append(encryptedDecryptedShares, encryptedDecryptedSharesForSecret)
	}

	return encryptedSecrets, encryptedDecryptedShares, nil
}

func (c *capability) Start(ctx context.Context) error {
	return nil
}

func (c *capability) RegisterToWorkflow(_ context.Context, rawRequest capabilities.RegisterToWorkflowRequest) error {
	c.lggr.Debugw("registering to workflow",
		"workflowID", sanitizeLogString(rawRequest.Metadata.WorkflowID),
		"workflowOwner", sanitizeLogString(rawRequest.Metadata.WorkflowOwner))
	return nil
}

func (c *capability) UnregisterFromWorkflow(_ context.Context, rawRequest capabilities.UnregisterFromWorkflowRequest) error {
	c.lggr.Debugw("unregistering from workflow",
		"workflowID", sanitizeLogString(rawRequest.Metadata.WorkflowID),
		"workflowOwner", sanitizeLogString(rawRequest.Metadata.WorkflowOwner))
	return nil
}

func (c *capability) Close() error {
	return nil
}
