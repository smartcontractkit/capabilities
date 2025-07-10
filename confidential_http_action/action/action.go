package target

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"google.golang.org/protobuf/proto"

	"github.com/go-viper/mapstructure/v2"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk"
	enclaveclient "github.com/smartcontractkit/confidential-compute/enclave-client"
	attestationvalidator "github.com/smartcontractkit/confidential-compute/enclave-client/attestation-validator"
	testdata "github.com/smartcontractkit/confidential-compute/enclave-client/test-data"
	httpenclavetypes "github.com/smartcontractkit/confidential-compute/enclave/nitro-confidential-http-enclave/types"
	enclavetypes "github.com/smartcontractkit/confidential-compute/types"
	"github.com/smartcontractkit/confidential-compute/util"
)

var (
	ID                         = "confidential-http-action@1.0.0"
	marshalFn                  = proto.Marshal
	unmarshalFn                = proto.Unmarshal
	confidentialHttpActionInfo = capabilities.MustNewCapabilityInfo(
		ID,
		capabilities.CapabilityTypeAction,
		"Executes an HTTP request confidentially, by perhaps using secrets from the VaultDON",
	)
)

type Params struct {
	Logger logger.Logger
	URL    string
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

func New(p Params) (*capability, error) {
	measurements, err := getMeasurements()
	if err != nil {
		return nil, fmt.Errorf("failed to get measurements: %w", err)
	}

	// Create a pool with a single enclave node.
	eID := sha256.Sum256([]byte("attestedhttp-action-enclave-id"))
	nodes := []enclavetypes.EnclaveNode{{
		EnclaveID:     eID,
		EnclaveURL:    p.URL,
		TrustedValues: measurements,
		EnclaveType:   enclavetypes.EnclaveTypeNitro,
	}}
	signer, err := util.NewSimpleEd25519Signer(p.SignerPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create signer: %w", err)
	}
	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // for demo purposes only.
			},
		},
	}
	pool, err := enclaveclient.NewPool[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse](nodes, p.PublicKey, signer, nil, nil, nil, &httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create enclave pool: %w", err)
	}
	return &capability{
		lggr:          p.Logger,
		enclaveClient: pool,
	}, nil
}

func (c *capability) Info(_ context.Context) (capabilities.CapabilityInfo, error) {
	return confidentialHttpActionInfo, nil
}

func (c *capability) Execute(ctx context.Context, rawRequest capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	c.lggr.Debugw("executing", "workflowID", rawRequest.Metadata.WorkflowID, "executionID", rawRequest.Metadata.WorkflowExecutionID, "workflowName", rawRequest.Metadata.WorkflowName, "workflowOwner", rawRequest.Metadata.WorkflowOwner)

	// Parse user input.
	if rawRequest.Inputs == nil {
		return capabilities.CapabilityResponse{}, errors.New("missing inputs field")
	}
	var input confidentialhttpactioncap.Input
	err := rawRequest.Inputs.UnwrapTo(&input)
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}

	// Fetch the public keys that can be used for this request.
	reqID := sha256.Sum256([]byte(rawRequest.Metadata.WorkflowExecutionID))
	ephemeralPubKeyResponse, err := c.enclaveClient.GetPublicKeys(ctx, reqID, nil)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to get public keys: %w", err)
	}

	// Out of the enclave responses returned, pick the first response (this capability only uses one enclave).
	// Then, select the most recently created public key from that enclave.
	if len(ephemeralPubKeyResponse) == 0 || len(ephemeralPubKeyResponse[0].PublicKeys) == 0 {
		return capabilities.CapabilityResponse{}, fmt.Errorf("no enclave public keys found for request %x", reqID)
	}
	selectedEnclaveResponse := ephemeralPubKeyResponse[0]
	var mostRecentPubKeyIndex, mostRecentPubKeyCreationTime int64
	for i, time := range selectedEnclaveResponse.CreationTimes {
		if time.UnixMicro() > mostRecentPubKeyCreationTime {
			mostRecentPubKeyIndex = int64(i)
		}
	}
	selectedEphemeralPublicKey := selectedEnclaveResponse.PublicKeys[mostRecentPubKeyIndex]
	c.lggr.Info(fmt.Sprintf("using enclave public key: %x for request %x", selectedEphemeralPublicKey, reqID))

	// Make requests to VaultDON here.

	var requests []httpenclavetypes.RequestTemplate
	err = mapstructure.Decode(input.Requests, &requests)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode requests: %w", err)
	}
	enclaveRequestData := httpenclavetypes.HTTPEnclaveRequestData{
		Requests:                requests,
		TemplateCiphertextNames: []string{}, // No ciphertexts are used in this capability.
	}

	// Execute the compute request.
	resp, err := c.enclaveClient.Execute(
		ctx,
		reqID,
		enclaveRequestData,
		[][]byte{},   // No ciphertexts are used in this capability.
		[][][]byte{}, // No encrypted decryption key shares are used in this capability.
		selectedEphemeralPublicKey,
		selectedEnclaveResponse.EnclaveID,
	)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to execute enclave request: %w", err)
	}

	// As with the enclave request data, output must be converted from the enclave type to the SDK type.
	var responses []confidentialhttpactioncap.Response
	err = mapstructure.Decode(resp.Output, &responses)
	if err != nil {
		return capabilities.CapabilityResponse{}, fmt.Errorf("failed to decode enclave response: %w", err)
	}

	var respBodies []string
	for _, r := range responses {
		respBodies = append(respBodies, string(r.Body))
	}

	// Validate the attestation here.

	// Return the response.
	valsMap, err := values.WrapMap(confidentialhttpactioncap.Output{
		Responses: responses,
	})
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}
	return capabilities.CapabilityResponse{
		Value: valsMap,
	}, nil
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

// Replace this with the actual implementation to get the measurements.
func getMeasurements() ([]byte, error) {
	emptyPCR, err := hex.DecodeString(testdata.EmptyPCR)
	if err != nil {
		return nil, err
	}
	trustedMeasurements := attestationvalidator.NitroPCRs{
		PCR0: emptyPCR,
		PCR1: emptyPCR,
		PCR2: emptyPCR,
	}
	trustedMeasurementsBin, err := json.Marshal(trustedMeasurements)
	if err != nil {
		log.Fatal(err)
	}

	return trustedMeasurementsBin, nil
}
