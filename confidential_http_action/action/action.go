package action

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

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

type VaultDON struct {
	MasterPublicKey       []byte
	CryptographyThreshold int
	PossibleFaultyNodes   int
	ID                    []byte
	Capability            capabilities.ExecutableCapability
}

type capability struct {
	lggr logger.Logger
	// These fields will be initialized lazily
	enclaveClient enclaveclient.EnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse]
	vaultDON      VaultDON
	keystore      core.Keystore

	// Fields required for lazy initialization
	initOnce            *sync.Once
	initializationError error
	capConfigRaw        string
	capabilityRegistry  core.CapabilitiesRegistry
}

// Config corresponds to the JSON schema field "Config".
type EnclavesConfig struct {
	// Enclaves corresponds to the JSON schema field "Enclaves".
	Enclaves []Enclave `json:"Enclaves" yaml:"Enclaves" mapstructure:"Enclaves"`
}

// Enclave corresponds to the JSON schema field "Enclave".
type Enclave struct {
	// EnclaveType corresponds to the JSON schema field "EnclaveType".
	EnclaveType *string `json:"EnclaveType,omitempty" yaml:"EnclaveType,omitempty" mapstructure:"EnclaveType,omitempty"`

	// ExtraData corresponds to the JSON schema field "ExtraData".
	ExtraData []uint8 `json:"ExtraData" yaml:"ExtraData" mapstructure:"ExtraData"`

	// ID corresponds to the JSON schema field "ID".
	ID []uint8 `json:"ID" yaml:"ID" mapstructure:"ID"`

	// TrustedValues corresponds to the JSON schema field "TrustedValues".
	TrustedValues []uint8 `json:"TrustedValues" yaml:"TrustedValues" mapstructure:"TrustedValues"`

	// URL corresponds to the JSON schema field "URL".
	URL string `json:"URL" yaml:"URL" mapstructure:"URL"`
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

// GetEnclaveNodes transforms a slice of Enclave structs from the config into a slice of EnclaveNode structs.
func GetEnclaveNodes(localNodeConfigBytes []byte) ([]enclavetypes.EnclaveNode, error) {
	var enclavesConfig EnclavesConfig
	err := json.Unmarshal(localNodeConfigBytes, &enclavesConfig)
	if err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %v", err)
	}

	// Pre-allocate the slice with the correct capacity for efficiency.
	nodes := make([]enclavetypes.EnclaveNode, 0, len(enclavesConfig.Enclaves))

	for i, confEnclave := range enclavesConfig.Enclaves {
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
			TrustedValues:    confEnclave.TrustedValues, // Directly compatible
			EnclaveExtraData: confEnclave.ExtraData,     // Directly compatible
			EnclaveType:      *enclaveType,
			EnclaveID:        enclaveID,
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// A generic function to get a value from the configuration. It takes the key and the expected type as a string for error messages.
func getValueFromConfig[T any](config capabilities.CapabilityConfiguration, key string) (T, error) {
	var zero T // A zero-value of type T to return on error

	if config.DefaultConfig == nil {
		return zero, fmt.Errorf("config.DefaultConfig is nil, cannot retrieve '%s'", key)
	}

	val, ok := config.DefaultConfig.Underlying[key]
	if !ok {
		return zero, fmt.Errorf("'%s' key not found in DefaultConfig", key)
	}

	// Unwrap the Value interface
	unwrappedValue, err := val.Unwrap()
	if err != nil {
		return zero, fmt.Errorf("error unwrapping '%s': %w", key, err)
	}

	// Type assertion to the generic type T
	finalValue, ok := unwrappedValue.(T)
	if !ok {
		return zero, fmt.Errorf("'%s' unwrapped to unexpected type: %T, expected %T", key, unwrappedValue, zero)
	}

	return finalValue, nil
}

func getVaultDONMasterPublicKey(vaultDONCapConfig capabilities.CapabilityConfiguration) ([]byte, error) {
	return getValueFromConfig[[]byte](vaultDONCapConfig, "masterPublicKey")
}

func getThreshold(vaultDONCapConfig capabilities.CapabilityConfiguration) (int, error) {
	return getValueFromConfig[int](vaultDONCapConfig, "threshold")
}

func getVaultDONPossibleFaultyNodes(ctx context.Context, vaultDONCapability capabilities.ExecutableCapability) (int, error) {
	capabilityInfo, err := vaultDONCapability.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get VaultDON capability info: %w", err)
	}
	return int(capabilityInfo.DON.F), nil
}

// New is the primary constructor for live use. It stores the dependencies
// required for lazy initialization.
func New(
	lggr logger.Logger,
	capConfigRaw string,
	keystore core.Keystore,
	capabilityRegistry core.CapabilitiesRegistry,
) *capability {
	return &capability{
		lggr:               lggr,
		keystore:           keystore,
		capConfigRaw:       capConfigRaw,
		capabilityRegistry: capabilityRegistry,
		initOnce:           &sync.Once{},
	}
}

// NewTest is a test-specific constructor that allows injecting pre-initialized components.
// It bypasses the lazy initialization logic entirely, making unit testing straightforward.
func NewTest(
	lggr logger.Logger,
	keystore core.Keystore,
	enclaveClient enclaveclient.EnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse],
	vaultDON VaultDON,
) *capability {
	var initOnce sync.Once
	initOnce.Do(func() {})

	return &capability{
		lggr:          lggr,
		keystore:      keystore,
		enclaveClient: enclaveClient,
		vaultDON:      vaultDON,
		initOnce:      &initOnce,
	}
}

// initLazily performs the actual initialization only once.
func (c *capability) initLazily(ctx context.Context) error {
	c.initOnce.Do(func() {
		// Check for an already existing initialization error
		if c.initializationError != nil {
			return
		}

		var capConfig cap.Config
		if err := json.Unmarshal([]byte(c.capConfigRaw), &capConfig); err != nil {
			c.initializationError = fmt.Errorf("failed to unmarshal config: %w", err)
			return
		}

		localNode, err := c.capabilityRegistry.LocalNode(ctx)
		if err != nil {
			c.initializationError = fmt.Errorf("failed to get local node: %w", err)
			return
		}
		if localNode.WorkflowDON.ID == 0 {
			c.initializationError = fmt.Errorf("local node does not have a WorkflowDON ID, cannot initialise confidential http action capability")
			return
		}
		localNodeConfigBytes := localNode.WorkflowDON.Config

		nodes, err := GetEnclaveNodes(localNodeConfigBytes)
		if err != nil {
			c.initializationError = fmt.Errorf("failed to create enclave pool: %w", err)
			return
		}

		if len(capConfig.VaultDONID) == 0 {
			c.initializationError = fmt.Errorf("VaultDONID must be provided in capability config to retrieve VaultDON capability")
			return
		}

		vaultDONIDStr := string(capConfig.VaultDONID)
		vaultDONIDUint, err := strconv.ParseUint(vaultDONIDStr, 10, 32)
		if err != nil {
			c.initializationError = fmt.Errorf("failed to parse VaultDONID '%s' as uint32: %w", vaultDONIDStr, err)
			return
		}

		vaultDONCapability, err := c.capabilityRegistry.GetExecutable(ctx, vault.CapabilityID)
		if err != nil {
			c.initializationError = fmt.Errorf("failed to get VaultDON capability with ID '%s' from registry: %w", vault.CapabilityID, err)
			return
		}

		vaultDONCapConfig, err := c.capabilityRegistry.ConfigForCapability(ctx, vault.CapabilityID, uint32(vaultDONIDUint))
		if err != nil {
			c.initializationError = fmt.Errorf("failed to get VaultDON config: %w", err)
			return
		}

		vaultDONMasterPublicKey, err := getVaultDONMasterPublicKey(vaultDONCapConfig)
		if err != nil {
			c.initializationError = fmt.Errorf("failed to get VaultDON master public key: %w", err)
			return
		}

		vaultDonThreshold, err := getThreshold(vaultDONCapConfig)
		if err != nil {
			c.initializationError = fmt.Errorf("failed to get VaultDON threshold: %w", err)
			return
		}

		vaultDONPossibleFaultyNodes, err := getVaultDONPossibleFaultyNodes(ctx, vaultDONCapability)
		if err != nil {
			c.initializationError = fmt.Errorf("failed to get VaultDON possible faulty nodes: %w", err)
			return
		}

		pool, err := enclaveclient.NewPool[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse](
			nodes, vaultDONMasterPublicKey, nil, nil, nil, nil, nil,
		)
		if err != nil {
			c.initializationError = fmt.Errorf("failed to create enclave pool: %w", err)
			return
		}

		c.enclaveClient = pool
		c.vaultDON = VaultDON{
			MasterPublicKey:       vaultDONMasterPublicKey,
			CryptographyThreshold: vaultDonThreshold,
			PossibleFaultyNodes:   vaultDONPossibleFaultyNodes,
			ID:                    capConfig.VaultDONID,
			Capability:            vaultDONCapability,
		}
	})
	return c.initializationError
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
	// Lazy initialization happens here
	if err := c.initLazily(ctx); err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	c.lggr.Debugw("executing", "workflowID", sanitizeLogString(rawRequest.Metadata.WorkflowID), "executionID", sanitizeLogString(rawRequest.Metadata.WorkflowExecutionID), "workflowName", sanitizeLogString(rawRequest.Metadata.WorkflowName), "workflowOwner", sanitizeLogString(rawRequest.Metadata.WorkflowOwner))

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
	if err := rawRequest.Inputs.UnwrapTo(&input); err != nil {
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

	encryptedSecrets, encryptedDecryptionShares, err := c.GetEncryptedDecryptionShares(
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
		MasterPublicKey:              c.vaultDON.MasterPublicKey,
		EnclaveEphemeralPublicKey:    enclaveParams.EnclaveEphemeralPublicKey,
		EncryptedDecryptionKeyShares: encryptedDecryptionShares,
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
		var prefix string
		if secret.Namespace != "" {
			prefix = secret.Namespace + "."
		}
		inputCiphertextNames = append(inputCiphertextNames, prefix+secret.Key)
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

func (c *capability) GetEncryptedDecryptionShares(
	ctx context.Context,
	vaultDONSecrets []cap.SecretIdentifier,
	enclaveEphemeralPublicKey []byte,
	workflowOwner string,
) ([][]byte, [][][]byte, error) {
	c.lggr.Debugw("Attempting to get encrypted decrypted shares from VaultDON capability",
		"vaultDONID", fmt.Sprintf("%x", c.vaultDON.ID),
		"enclaveEphemeralPublicKey", fmt.Sprintf("%x", enclaveEphemeralPublicKey[:8])) // Log first 8 bytes for brevity

	if c.vaultDON.Capability == nil {
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
	vaultDONResponse, err := c.vaultDON.Capability.Execute(ctx, vaultDONRequest)
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
	encryptedDecryptionShares := make([][][]byte, 0)

	for _, secretResp := range vaultDONOutput.Responses {
		if secretResp.GetError() != "" {
			return nil, nil, fmt.Errorf("VaultDON returned an error for secret %s: %s", secretResp.GetId().GetKey(), secretResp.GetError())
		}

		secretData := secretResp.GetData()
		if secretData == nil {
			return nil, nil, fmt.Errorf("VaultDON returned no data for secret %s", secretResp.GetId().GetKey())
		}

		encryptedSecrets = append(encryptedSecrets, []byte(secretData.GetEncryptedValue()))
		encryptedDecryptionSharesForSecret := make([][]byte, 0)

		if len(secretData.GetEncryptedDecryptionKeyShares()) != 1 {
			return nil, nil, fmt.Errorf("expected exactly one set of encrypted decryption key shares for secret %s, got %d", secretResp.GetId().GetKey(), len(secretData.GetEncryptedDecryptionKeyShares()))
		}
		for _, shareStr := range secretData.GetEncryptedDecryptionKeyShares()[0].GetShares() {
			encryptedDecryptionSharesForSecret = append(encryptedDecryptionSharesForSecret, []byte(shareStr))
		}
		minimumSharesRequired := c.vaultDON.CryptographyThreshold + c.vaultDON.PossibleFaultyNodes
		if len(encryptedDecryptionSharesForSecret) < minimumSharesRequired {
			return nil, nil, fmt.Errorf("not enough encrypted decryption key shares for secret %s, expected at least %d, got %d", secretResp.GetId().GetKey(), minimumSharesRequired, len(encryptedDecryptionSharesForSecret))
		}
		encryptedDecryptionShares = append(encryptedDecryptionShares, encryptedDecryptionSharesForSecret)
	}

	return encryptedSecrets, encryptedDecryptionShares, nil
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
