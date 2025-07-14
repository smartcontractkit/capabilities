package target

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"strings"

	cap "github.com/smartcontractkit/capabilities/confidential_http_action/confidential_http_action_cap"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk"
	enclaveclient "github.com/smartcontractkit/confidential-compute/enclave-client"
	httpenclavetypes "github.com/smartcontractkit/confidential-compute/enclave/nitro-confidential-http-enclave/types"
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

type Request struct {
	Metadata capabilities.RequestMetadata
	Config   *values.Map
	Inputs   sdk.CapMap
}

type capability struct {
	lggr          logger.Logger
	enclaveClient enclaveclient.EnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse]
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

// SignerCapability is (for now) a no-op implementation of the Signer interface. This will be replaced with a call to the p2pSigner capability.
type SignerCapability struct{}

func (s *SignerCapability) Sign(data []byte) ([]byte, error) {
	return data, nil
}

func New(lggr logger.Logger, c cap.Config) (*capability, error) {
	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // for demo purposes only.
			},
		},
	}
	nodes, err := GetNodes(c)
	if err != nil {
		return nil, fmt.Errorf("failed to create enclave pool: %w", err)
	}

	vaultDONPublicKey, err := getVaultDONPublicKey(c.VaultDONID)

	if err != nil {
		return nil, fmt.Errorf("failed to get VaultDON public key: %w", err)
	}

	pool, err := enclaveclient.NewPool[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse](nodes, vaultDONPublicKey, &SignerCapability{}, nil, nil, nil, &httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create enclave pool: %w", err)
	}
	return &capability{
		lggr:          lggr,
		enclaveClient: pool,
	}, nil
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.lggr.Debugw("executing", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner)
	return capabilities.CapabilityResponse{}, nil
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
