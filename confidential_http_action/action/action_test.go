package action_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/actions/vault"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	httpenclavetypes "github.com/smartcontractkit/confidential-compute/enclave/nitro-confidential-http-enclave/types"
	enclavetypes "github.com/smartcontractkit/confidential-compute/types"

	action "github.com/smartcontractkit/capabilities/confidential_http_action/action"
	cap "github.com/smartcontractkit/capabilities/confidential_http_action/confidential_http_action_cap"
	"github.com/smartcontractkit/capabilities/libs/testutils"
)

// MockEnclaveClient is a simple struct that implements the EnclaveClient interface.
type MockEnclaveClient[PublicDataType any, OutputType any] struct {
	GetPublicKeysFunc func(ctx context.Context, requestID [32]byte, enclaveSpecifications []byte) ([]enclavetypes.EnclavePublicKeyData, error)

	// The ExecuteFunc's signature now correctly uses the struct's own type parameters
	ExecuteFunc func(ctx context.Context, requestID [32]byte, publicData PublicDataType, ciphertexts [][]byte, encryptedDecryptionKeyShares [][][]byte, enclaveEphemeralPublicKey []byte, enclaveID [32]byte) (*enclavetypes.ExecuteResponse[OutputType], error)

	ExecuteBatchFunc func(ctx context.Context, reqs []enclavetypes.SignedComputeRequest, enclaveIDs [][32]byte) ([]enclavetypes.RawExecuteResponse, error)

	UpdateNodesFunc func(nodes []enclavetypes.EnclaveNode)
}

func (m *MockEnclaveClient[PublicDataType, OutputType]) GetPublicKeys(ctx context.Context, requestID [32]byte, enclaveSpecifications []byte) ([]enclavetypes.EnclavePublicKeyData, error) {
	return []enclavetypes.EnclavePublicKeyData{
		{
			PublicKeyResponse: enclavetypes.PublicKeyResponse{
				PublicKeys: [][]byte{
					[]byte("mock_public_key_bytes_1"),
					[]byte("mock_public_key_bytes_2"),
				},
				CreationTimes: []time.Time{time.Now(), time.Now().Add(-time.Hour)},
				TTLs:          []time.Duration{time.Hour * 24, time.Hour * 48},
				Config:        enclavetypes.EnclaveConfig{},
				Attestation:   []byte("mock_attestation_data"),
			},
			EnclaveID: [32]byte{9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
		},
	}, nil
}

func (m *MockEnclaveClient[PublicDataType, OutputType]) Execute(ctx context.Context, requestID [32]byte, publicData PublicDataType, ciphertexts [][]byte, encryptedDecryptionKeyShares [][][]byte, enclaveEphemeralPublicKey []byte, enclaveID [32]byte) (*enclavetypes.ExecuteResponse[OutputType], error) {
	if m.ExecuteFunc != nil {
		return m.ExecuteFunc(ctx, requestID, publicData, ciphertexts, encryptedDecryptionKeyShares, enclaveEphemeralPublicKey, enclaveID)
	}
	return nil, nil
}

func (m *MockEnclaveClient[PublicDataType, OutputType]) ExecuteBatch(ctx context.Context, reqs []enclavetypes.SignedComputeRequest, enclaveIDs [][32]byte) ([]enclavetypes.RawExecuteResponse, error) {
	if m.ExecuteBatchFunc != nil {
		return m.ExecuteBatchFunc(ctx, reqs, enclaveIDs)
	}
	return nil, nil
}

func (m *MockEnclaveClient[PublicDataType, OutputType]) commonExecuteBatchReturn(t *testing.T) ([]enclavetypes.RawExecuteResponse, error) {
	mockResponsesSlice := []enclavetypes.HTTPResponse{
		{StatusCode: 200, Body: []byte("First response")},
		{StatusCode: 500, Body: []byte("Second response")},
	}
	innerJSONBytes, err := json.Marshal(mockResponsesSlice)
	assert.NoError(t, err)

	return []enclavetypes.RawExecuteResponse{
		{
			RequestID: [32]byte{1, 2, 3},
			Output:    innerJSONBytes,
			Config: enclavetypes.EnclaveConfig{
				Signers:         [][]byte{[]byte("signer_key_1"), []byte("signer_key_2")},
				MasterPublicKey: []byte("master_pub_key_1"),
				T:               1,
				F:               0,
			},
			Attestation: []byte(""),
		}}, nil
}

func (m *MockEnclaveClient[PublicDataType, OutputType]) UpdateNodes(nodes []enclavetypes.EnclaveNode) {
	if m.UpdateNodesFunc != nil {
		m.UpdateNodesFunc(nodes)
	}
}

type mockKeystore struct {
	accounts     []string
	signFunc     func(ctx context.Context, account string, msg []byte) ([]byte, error)
	accountsFunc func(ctx context.Context) ([]string, error)
}

func (m *mockKeystore) Accounts(ctx context.Context) ([]string, error) {
	if m.accountsFunc != nil {
		return m.accountsFunc(ctx)
	}
	return m.accounts, nil
}

func (m *mockKeystore) Sign(ctx context.Context, account string, msg []byte) ([]byte, error) {
	if m.signFunc != nil {
		return m.signFunc(ctx, account, msg)
	}
	return []byte(""), nil
}

func getMockKeystore() *mockKeystore {
	return &mockKeystore{
		accounts: []string{core.P2PAccountKey},
		signFunc: func(ctx context.Context, account string, msg []byte) ([]byte, error) {
			return []byte("test-signature"), nil
		},
	}
}

// MockVaultDONCapability implements core.ExecutableCapability for testing VaultDON interactions.
type MockVaultDONCapability struct {
	ExecuteFunc func(ctx context.Context, req capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error)
}

// Info implements core.ExecutableCapability.
func (m *MockVaultDONCapability) Info(ctx context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.CapabilityInfo{
		ID:             "mock-vault-don@1.0.0",
		CapabilityType: capabilities.CapabilityTypeAction,
		Description:    "Mock VaultDON for testing",
	}, nil
}

// Execute implements core.ExecutableCapability.
func (m *MockVaultDONCapability) Execute(ctx context.Context, req capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	if m.ExecuteFunc != nil {
		return m.ExecuteFunc(ctx, req)
	}
	return capabilities.CapabilityResponse{}, errors.New("ExecuteFunc not implemented for MockVaultDONCapability")
}

// RegisterToWorkflow implements core.ExecutableCapability.
func (m *MockVaultDONCapability) RegisterToWorkflow(ctx context.Context, request capabilities.RegisterToWorkflowRequest) error {
	return nil // Not used in this test scenario
}

// UnregisterFromWorkflow implements core.ExecutableCapability.
func (m *MockVaultDONCapability) UnregisterFromWorkflow(ctx context.Context, request capabilities.UnregisterFromWorkflowRequest) error {
	return nil // Not used in this test scenario
}

func getTestConfig() cap.Config {
	enclaveTypeA := "TypeA"

	return cap.Config{
		Enclaves: []cap.Enclave{
			{
				EnclaveType:   &enclaveTypeA,
				ExtraData:     []uint8{0x01, 0x02, 0x03},
				ID:            []uint8{0xAA, 0xBB},
				TrustedValues: []uint8{0xDE, 0xAD, 0xBE, 0xEF},
				URL:           "http://enclave-a.example.com",
			},
			{
				EnclaveType:   nil, // Omitting EnclaveType for this one
				ExtraData:     []uint8{0x04, 0x05},
				ID:            []uint8{0xCC, 0xDD},
				TrustedValues: []uint8{0x11, 0x22, 0x33},
				URL:           "https://enclave-b.example.com/api",
			},
		},
		VaultDONID: []uint8{0xF0, 0x0B, 0xAA, 0x42},
	}
}

func getTestInput() cap.Input {
	return cap.Input{
		Requests: []cap.Request{
			{
				Url:    "https://api.example.com/status",
				Method: "GET",
				Headers: []string{
					"Content-Type: application/json",
				},
				Body: "",
			},
		},
		VaultDONSecretIds: []string{"my-secret-api-key"},
	}
}

func setupAndExecuteAction(t *testing.T, mockEnclaveClient *MockEnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse], mockVaultDON *MockVaultDONCapability) (capabilities.CapabilityResponse, error) {
	c, err := action.NewWithEnclaveClient(
		logger.Test(t),
		getTestConfig(),
		getMockKeystore(),
		mockEnclaveClient,
		[]byte{0xDE, 0xAD, 0xBE, 0xEF}, // vaultDONMasterPublicKey
		mockVaultDON,
	)
	require.NoError(t, err)

	input := getTestInput()

	inputsValue, err := values.WrapMap(input)
	require.NoError(t, err)

	ctx := context.Background()
	workflow, removeWorkflow := testutils.NewWorkflow(ctx, testutils.WorkflowParams{
		T: t,
		Capabilities: []testutils.CapabilityWithConfig{
			{
				Capability: c,
			},
		},
		Owner: "owner1",
	})
	defer removeWorkflow(ctx)

	req := capabilities.CapabilityRequest{
		Inputs: inputsValue,
		Metadata: capabilities.RequestMetadata{
			WorkflowID:          workflow.ID,
			WorkflowExecutionID: "",
			WorkflowName:        "",
			WorkflowOwner:       workflow.Owner,
		},
	}

	return c.Execute(context.Background(), req)
}

func TestNew(t *testing.T) {
	t.Run("a new confidential http capability action is created", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		mockVaultDON := &MockVaultDONCapability{}
		c, err := action.New(logger.Test(t), getTestConfig(), mockKeystore, mockVaultDON, []byte{0xDE, 0xAD, 0xBE, 0xEF})
		assert.NoError(t, err)
		assert.NotNil(t, c)
	})
}

func TestCapability_Info(t *testing.T) {
	t.Run("capability info is reported correctly", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		mockVaultDON := &MockVaultDONCapability{}
		c, err := action.New(logger.Test(t), getTestConfig(), mockKeystore, mockVaultDON, []byte{0xDE, 0xAD, 0xBE, 0xEF})
		assert.NoError(t, err)
		info, err := c.Info(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "confidential-http-action@1.0.0", info.ID)
		assert.Equal(t, capabilities.CapabilityType("action"), info.CapabilityType)
		assert.Equal(t, "Executes an HTTP request confidentially, by perhaps using secrets from the VaultDON", info.Description)
		assert.Equal(t, true, info.IsLocal)
	})
}

func TestCapability_Execute(t *testing.T) {
	t.Run("capability executes without error", func(t *testing.T) {
		mockVaultDON := &MockVaultDONCapability{}
		mockVaultDON.ExecuteFunc = func(ctx context.Context, req capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
			assert.Equal(t, vault.MethodGetSecrets, req.Method, "Expected VaultDON method to be GetSecrets")

			var getSecretsReq vault.GetSecretsRequest
			err := req.Payload.UnmarshalTo(&getSecretsReq)
			require.NoError(t, err, "Failed to unmarshal VaultDON request payload")

			assert.Len(t, getSecretsReq.Requests, 1, "Expected one secret request")
			assert.Equal(t, "my-secret-api-key", getSecretsReq.Requests[0].GetId().GetKey(), "Expected secret ID to match")
			assert.Len(t, getSecretsReq.Requests[0].GetEncryptionKeys(), 1, "Expected one encryption key")
			assert.Equal(t, string([]byte("mock_public_key_bytes_1")), getSecretsReq.Requests[0].GetEncryptionKeys()[0], "Expected encryption key to match enclave public key")

			// Simulate VaultDON response
			mockEncryptedSecretValue := "encrypted_secret_data_for_my-secret-id"
			mockEncryptedDecryptionKeyShare1 := "share1_for_my-secret-id"
			mockEncryptedDecryptionKeyShare2 := "share2_for_my-secret-id"

			vaultDONResponsePayload := &vault.GetSecretsResponse{
				Responses: []*vault.SecretResponse{
					{
						Id: &vault.SecretIdentifier{
							Key:       "my-secret-api-key",
							Namespace: "",
							Owner:     "",
						},
						Result: &vault.SecretResponse_Data{
							Data: &vault.SecretData{
								EncryptedValue: mockEncryptedSecretValue,
								EncryptedDecryptionKeyShares: []*vault.EncryptedShares{
									{
										Shares:        []string{mockEncryptedDecryptionKeyShare1, mockEncryptedDecryptionKeyShare2},
										EncryptionKey: string([]byte("mock_public_key_bytes_1")),
									},
								},
							},
						},
					},
				},
			}

			respAny, err := anypb.New(vaultDONResponsePayload)
			require.NoError(t, err, "Failed to marshal VaultDON response payload to Any")

			return capabilities.CapabilityResponse{
				Payload: respAny,
			}, nil
		}

		mockEnclaveClient := &MockEnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse]{}

		mockEnclaveClient.ExecuteBatchFunc = func(ctx context.Context, reqs []enclavetypes.SignedComputeRequest, enclaveIDs [][32]byte) ([]enclavetypes.RawExecuteResponse, error) {
			assert.Equal(t, len(reqs), 1, "Expected one signed compute request")
			assert.Equal(t, 1, len(reqs[0].Ciphertexts), "Expected one ciphertext in the request")
			assert.Equal(t, 1, len(reqs[0].EncryptedDecryptionKeyShares), "Expected 1 set of shares in the request")
			assert.Equal(t, 2, len(reqs[0].EncryptedDecryptionKeyShares[0]), "Expected 2 shares per ciphertext in the request")
			assert.Equal(t, []byte("test-signature"), reqs[0].Signature, "Expected secret ID to match")
			return mockEnclaveClient.commonExecuteBatchReturn(t)
		}

		resp, err := setupAndExecuteAction(t, mockEnclaveClient, mockVaultDON)
		assert.NoError(t, err)
		assert.NotNil(t, resp.Value)

		var capOutput cap.Output
		err = resp.Value.UnwrapTo(&capOutput)
		assert.NoError(t, err)

		assert.Equal(t, 2, len(capOutput.Responses), "Expected two responses in the output")
		var actualStatusCodes []int64
		for _, res := range capOutput.Responses {
			actualStatusCodes = append(actualStatusCodes, res.StatusCode)
		}
		expectedStatusCodes := []int64{200, 500}
		assert.ElementsMatch(t, expectedStatusCodes, actualStatusCodes, "StatusCodes do not match expected values")

		var actualResponseBytes [][]byte
		for _, res := range capOutput.Responses {
			actualResponseBytes = append(actualResponseBytes, res.Body)
		}
		expectedResponseBytes := [][]byte{[]byte("First response"), []byte("Second response")}
		assert.ElementsMatch(t, expectedResponseBytes, actualResponseBytes, "Response bodies do not match expected values")
	})

	t.Run("capability executes with error from VaultDON", func(t *testing.T) {
		mockEnclaveClient := &MockEnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse]{}

		// --- Mock VaultDON Capability that returns an error ---
		mockVaultDON := &MockVaultDONCapability{}
		mockVaultDON.ExecuteFunc = func(ctx context.Context, req capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
			return capabilities.CapabilityResponse{}, errors.New("simulated VaultDON error")
		}

		_, err := setupAndExecuteAction(t, mockEnclaveClient, mockVaultDON)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get encrypted decryption key shares from VaultDON: failed to execute VaultDON capability: simulated VaultDON error")
	})

	t.Run("capability executes with VaultDON returning secret error", func(t *testing.T) {
		mockEnclaveClient := &MockEnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse]{}

		mockEnclaveClient.ExecuteBatchFunc = func(ctx context.Context, reqs []enclavetypes.SignedComputeRequest, enclaveIDs [][32]byte) ([]enclavetypes.RawExecuteResponse, error) {
			assert.Equal(t, len(reqs), 1, "Expected one signed compute request")
			assert.Equal(t, 0, len(reqs[0].Ciphertexts), "Expected no ciphertexts in the request")
			assert.Equal(t, 0, len(reqs[0].EncryptedDecryptionKeyShares), "Expected no shares in the request")
			return mockEnclaveClient.commonExecuteBatchReturn(t)
		}

		// --- Mock VaultDON Capability that returns a secret error ---
		mockVaultDON := &MockVaultDONCapability{}
		mockVaultDON.ExecuteFunc = func(ctx context.Context, req capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
			vaultDONResponsePayload := &vault.GetSecretsResponse{
				Responses: []*vault.SecretResponse{
					{
						Id: &vault.SecretIdentifier{
							Key: "my-secret-id",
						},
						Result: &vault.SecretResponse_Error{
							Error: "secret not found",
						},
					},
				},
			}

			respAny, err := anypb.New(vaultDONResponsePayload)
			require.NoError(t, err)

			return capabilities.CapabilityResponse{
				Payload: respAny,
			}, nil
		}

		resp, err := setupAndExecuteAction(t, mockEnclaveClient, mockVaultDON)
		assert.NoError(t, err) // No error at this level, but the secret won't be in the final ComputeRequest
		assert.NotNil(t, resp.Value)

		var capOutput cap.Output
		err = resp.Value.UnwrapTo(&capOutput)
		assert.NoError(t, err)

		// To assert that no encrypted secrets or shares were added because of the error, we check the mockEnclaveClient higher up in this test.
	})
}
