package action

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/mitchellh/mapstructure"
	cap "github.com/smartcontractkit/capabilities/confidential_http_action/confidential_http_action_cap"
	enclaveclient "github.com/smartcontractkit/confidential-compute/enclave-client"
	httpenclavetypes "github.com/smartcontractkit/confidential-compute/enclave/nitro-confidential-http-enclave/types"
	"github.com/smartcontractkit/confidential-compute/types"
	enclavetypes "github.com/smartcontractkit/confidential-compute/types"
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
	lggr              logger.Logger
	enclaveClient     enclaveclient.EnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse]
	keystore          core.Keystore
	vaultDonPublicKey []byte
	vaultDonID        []byte
}

// parseEnclaveType converts a string into an EnclaveType using case-insensitive matching.
// It handles the case where the source type is a pointer and might be nil.
// It defaults to AWS NITRO.
func parseEnclaveType(typeStr *string) enclavetypes.EnclaveType {
	if typeStr == nil {
		return enclavetypes.EnclaveTypeNitro
	}

	// Convert input to upper case for case-insensitive matching.
	upperType := strings.ToUpper(*typeStr)
	switch enclavetypes.EnclaveType(upperType) {
	case enclavetypes.EnclaveTypeNitro, enclavetypes.EnclaveTypeSGX, enclavetypes.EnclaveTypeTDX, enclavetypes.EnclaveTypeSEV:
		return enclavetypes.EnclaveType(upperType)
	default:
		return enclavetypes.EnclaveTypeNitro // Default to AWS NITRO if no match found.
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

		node := enclavetypes.EnclaveNode{
			EnclaveURL:       confEnclave.URL,
			TrustedValues:    confEnclave.TrustedValues, // Directly compatible ( []uint8 is alias for []byte )
			EnclaveExtraData: confEnclave.ExtraData,     // Directly compatible
			EnclaveType:      parseEnclaveType(confEnclave.EnclaveType),
			EnclaveID:        enclaveID,
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// getVaultDONPublicKey is a placeholder function to simulate retrieving a DON's public key.
// In a real implementation, this would involve a lookup process based on the DON ID.
// For now, it returns a hardcoded public key to allow the program to compile.
func getVaultDONPublicKey(donID []byte) ([]byte, error) {
	// This is a dummy public key for placeholder purposes.
	// It's 32 bytes long, which is the standard length for an ed25519 public key.
	dummyPublicKey := []byte{
		0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
		0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
		0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
		0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
	}

	// You could add logic here to check the donID if needed.
	if len(donID) == 0 {
		return nil, fmt.Errorf("VaultDONID cannot be empty")
	}

	log.Printf("Placeholder: Looked up public key for DON ID: %x\n", donID)

	return dummyPublicKey, nil
}

// Query the VaultDON to get the encrypted decryption key shares.
// This is a placeholder function that simulates the process of getting encrypted shares.
func GetEncryptedDecryptedShares(vaultDonSecretIds []string, vaultDONPublicKey []byte, vaultDonID []byte) ([][]byte, [][][]byte, error) {
	encryptedDecryptedShares := make([][][]byte, len(vaultDonSecretIds))
	encryptedSecrets := make([][]byte, len(vaultDonSecretIds))
	return encryptedSecrets, encryptedDecryptedShares, nil
}

func New(lggr logger.Logger, capConfig cap.Config, keystore core.Keystore) (*capability, error) {
	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: false,
			},
		},
	}
	nodes, err := GetNodes(capConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create enclave pool: %w", err)
	}

	vaultDONPublicKey, err := getVaultDONPublicKey(capConfig.VaultDONID)

	if err != nil {
		return nil, fmt.Errorf("failed to get VaultDON public key: %w", err)
	}

	// Setting Signer to nil for now, as we plan to use only enclaveClient.executeBatch in this capability.
	// The other nils are ok as they default to reasonable implementaitons in the enclaveClient.
	pool, err := enclaveclient.NewPool[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse](nodes, vaultDONPublicKey, nil, nil, nil, nil, &httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create enclave pool: %w", err)
	}
	return &capability{
		lggr:              lggr,
		enclaveClient:     pool,
		keystore:          keystore,
		vaultDonPublicKey: vaultDONPublicKey,
		vaultDonID:        capConfig.VaultDONID,
	}, nil
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

	publicData := ConvertInputToHTTPEnclaveRequestData(input)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to convert input to HTTP enclave request data: %w", err)
	}
	publicDataBytes, err := json.Marshal(publicData)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to marshal templates: %w", err)
	}

	encryptedSecrets, encryptedDecyrptedShares, err := GetEncryptedDecryptedShares(input.VaultDonSecretIds, c.vaultDonPublicKey, c.vaultDonID)

	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to get encrypted decryption key shares: %w", err)
	}

	computeReq := types.ComputeRequest{
		RequestID:                    reqID,
		PublicData:                   publicDataBytes,
		Ciphertexts:                  encryptedSecrets,
		MasterPublicKey:              c.vaultDonPublicKey,
		EnclaveEphemeralPublicKey:    enclaveParams.EnclaveEphemeralPublicKey,
		EncryptedDecryptionKeyShares: encryptedDecyrptedShares,
	}
	signedComputeReq, err := c.SignComputeRequest(ctx, computeReq)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to sign compute request: %w", err)
	}

	rawExecuteResponses, err := c.enclaveClient.ExecuteBatch(ctx, []types.SignedComputeRequest{*signedComputeReq}, [][32]byte{enclaveParams.EnclaveID})
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to execute enclave request: %w", err)
	}

	if len(rawExecuteResponses) != 1 {
		return capabilities.CapabilityResponse{}, fmt.Errorf("expected one enclave response, got %d", len(rawExecuteResponses))
	}

	// As with the enclave request data, output must be converted from the enclave type to the SDK type.
	var responses []cap.OutputResponsesElem
	err = mapstructure.Decode(rawExecuteResponses[0].Output, &responses)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode enclave response: %w", err)
	}

	var respBodies []string
	for _, r := range responses {
		respBodies = append(respBodies, string(r.Body))
	}
	c.lggr.Infow("confidentialhttpcap capability has validated an attested batch of HTTP responses",
		"workflowID", rawRequest.Metadata.WorkflowID,
		"executionID", rawRequest.Metadata.WorkflowExecutionID,
		"workflowName", rawRequest.Metadata.WorkflowName,
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

	return &types.SignedComputeRequest{
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

func ConvertInputToHTTPEnclaveRequestData(input cap.Input) httpenclavetypes.HTTPEnclaveRequestData {
	convertedRequests := make([]httpenclavetypes.RequestTemplate, 0, len(input.Requests))
	for _, req := range input.Requests {
		convertedRequests = append(convertedRequests, httpenclavetypes.RequestTemplate{
			URL:                  req.Url,
			Method:               req.Method,
			Body:                 req.Body,
			Headers:              req.Headers,
			TemplatePublicValues: req.PublicTemplateValues,
			CustomRootCaCertPEM:  []byte(req.CustomRootCaCertPEM),
		})
	}

	return httpenclavetypes.HTTPEnclaveRequestData{
		Requests:                convertedRequests,
		TemplateCiphertextNames: input.VaultDonSecretIds,
	}
}

func (c *capability) Start(ctx context.Context) error {
	return nil
}

func (c *capability) RegisterToWorkflow(_ context.Context, rawRequest capabilities.RegisterToWorkflowRequest) error {
	c.lggr.Debugw("registering to workflow", "workflowID", rawRequest.Metadata.WorkflowID, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
	return nil
}

func (c *capability) UnregisterFromWorkflow(_ context.Context, rawRequest capabilities.UnregisterFromWorkflowRequest) error {
	c.lggr.Debugw("unregistering from workflow", "workflowID", rawRequest.Metadata.WorkflowID, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
	return nil
}

func (c *capability) Close() error {
	return nil
}
