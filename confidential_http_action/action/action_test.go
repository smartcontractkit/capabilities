package action_test

import (
	"context"
	"encoding/hex"
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
	return nil, errors.New("ExecuteFunc not implemented for MockEnclaveClient")
}

func (m *MockEnclaveClient[PublicDataType, OutputType]) ExecuteBatch(ctx context.Context, reqs []enclavetypes.SignedComputeRequest, enclaveIDs [][32]byte) ([]enclavetypes.RawExecuteResponse, error) {
	if m.ExecuteBatchFunc != nil {
		return m.ExecuteBatchFunc(ctx, reqs, enclaveIDs)
	}
	return nil, errors.New("ExecuteBatchFunc not implemented for MockEnclaveClient")
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

// UnregisterFromWorkflow implements the required method for capabilities.ExecutableCapability.
func (m *MockVaultDONCapability) UnregisterFromWorkflow(ctx context.Context, workflow capabilities.UnregisterFromWorkflowRequest) error {
	// No-op for testing
	return nil
}

// RegisterToWorkflow implements the required method for capabilities.ExecutableCapability.
func (m *MockVaultDONCapability) RegisterToWorkflow(ctx context.Context, workflow capabilities.RegisterToWorkflowRequest) error {
	// No-op for testing
	return nil
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

func getTestCapConfigString() string {
	capConfig := cap.Config{
		VaultDONID: []uint8{0xF0, 0x0B, 0xAA, 0x42},
	}
	capConfigBytes, _ := json.Marshal(capConfig)
	return string(capConfigBytes)
}

func getTestEnclavesConfigBytes() []byte {
	enclaveTypeNitro := "NITRO" // Example enclave type, can be changed to SGX, TDX, etc.

	enclavesConfig := action.EnclavesConfig{
		Enclaves: []action.Enclave{
			{
				EnclaveType:   &enclaveTypeNitro,
				ExtraData:     []uint8{0x01, 0x02, 0x03},
				ID:            []uint8{0xAA, 0xBB},
				TrustedValues: []uint8{0xDE, 0xAD, 0xBE, 0xEF},
				URL:           "http://enclave-a.example.com",
			},
			{
				EnclaveType:   &enclaveTypeNitro,
				ExtraData:     []uint8{0x04, 0x05},
				ID:            []uint8{0xCC, 0xDD},
				TrustedValues: []uint8{0x11, 0x22, 0x33},
				URL:           "https://enclave-b.example.com/api",
			},
		},
	}

	enclavesConfigBytes, _ := json.MarshalIndent(enclavesConfig, "", "  ")
	return enclavesConfigBytes
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
		VaultDONSecrets: []cap.SecretIdentifier{
			{
				Key:       "my-secret-api-key",
				Namespace: "my-namespace",
				Owner:     nil},
		},
	}
}

func setupAndExecuteAction(t *testing.T,
	mockEnclaveClient *MockEnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse],
	mockVaultDON action.VaultDON) (capabilities.CapabilityResponse, error) {
	c := action.NewTest(
		logger.Test(t),
		getMockKeystore(),
		mockEnclaveClient,
		mockVaultDON)

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

func TestCapability_Info(t *testing.T) {
	t.Run("capability info is reported correctly", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c := action.New(logger.Test(t), getTestCapConfigString(), mockKeystore, nil)
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
		mockVaultDONCapability := &MockVaultDONCapability{}
		mockVaultDONCapability.ExecuteFunc = func(ctx context.Context, req capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
			assert.Equal(t, vault.MethodGetSecrets, req.Method, "Expected VaultDON method to be GetSecrets")

			var getSecretsReq vault.GetSecretsRequest
			err := req.Payload.UnmarshalTo(&getSecretsReq)
			require.NoError(t, err, "Failed to unmarshal VaultDON request payload")

			assert.Len(t, getSecretsReq.Requests, 1, "Expected one secret request")
			assert.Equal(t, "my-secret-api-key", getSecretsReq.Requests[0].GetId().GetKey(), "Expected secret ID to match")
			assert.Len(t, getSecretsReq.Requests[0].GetEncryptionKeys(), 1, "Expected one encryption key")
			assert.Equal(t, hex.EncodeToString([]byte("mock_public_key_bytes_1")), getSecretsReq.Requests[0].GetEncryptionKeys()[0], "Expected encryption key to match enclave public key")

			vaultDONResponsePayload := &vault.GetSecretsResponse{
				Responses: []*vault.SecretResponse{
					{
						Id: &vault.SecretIdentifier{
							Key:       "my-secret-api-key",
							Namespace: "my-namespace",
							Owner:     "",
						},
						Result: &vault.SecretResponse_Data{
							Data: &vault.SecretData{
								EncryptedValue: "encrypted_secret_data_for_my-secret-id",
								EncryptedDecryptionKeyShares: []*vault.EncryptedShares{
									{
										Shares:        []string{hex.EncodeToString([]byte("share1_for_my-secret-id")), hex.EncodeToString([]byte("share2_for_my-secret-id"))},
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

			publicDataBytes := reqs[0].PublicData
			assert.NotNil(t, publicDataBytes, "Expected public data to be set in the request")
			var publicData httpenclavetypes.HTTPEnclaveRequestData
			err := json.Unmarshal(publicDataBytes, &publicData)
			assert.NoError(t, err, "Failed to unmarshal public data")
			assert.Equal(t, "my-namespace.my-secret-api-key", publicData.TemplateCiphertextNames[0], "Expected template ciphertext name to match")
			assert.Equal(t, "https://api.example.com/status", publicData.Requests[0].URL, "Expected request URL to match")
			assert.Equal(t, "GET", publicData.Requests[0].Method, "Expected request method to match")
			assert.Equal(t, "Content-Type: application/json", publicData.Requests[0].Headers[0], "Expected request header to match")
			assert.Equal(t, "", publicData.Requests[0].Body, "Expected request body to match encrypted secret data")

			return mockEnclaveClient.commonExecuteBatchReturn(t)
		}

		mockVaultDON := action.VaultDON{
			MasterPublicKey:       []byte{0xDE, 0xAD, 0xBE, 0xEF},
			CryptographyThreshold: 1,
			PossibleFaultyNodes:   0,
			ID:                    []uint8{0xF0, 0x0B, 0xAA, 0x42},
			Capability:            mockVaultDONCapability,
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
		mockVaultDONCapability := &MockVaultDONCapability{}
		mockVaultDONCapability.ExecuteFunc = func(ctx context.Context, req capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
			return capabilities.CapabilityResponse{}, errors.New("simulated VaultDON error")
		}

		mockVaultDON := action.VaultDON{
			MasterPublicKey:       []byte{0xDE, 0xAD, 0xBE, 0xEF},
			CryptographyThreshold: 1,
			PossibleFaultyNodes:   0,
			ID:                    []uint8{0xF0, 0x0B, 0xAA, 0x42},
			Capability:            mockVaultDONCapability,
		}

		_, err := setupAndExecuteAction(t, mockEnclaveClient, mockVaultDON)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get encrypted decryption key shares from VaultDON: failed to execute VaultDON capability: simulated VaultDON error")
	})

	t.Run("capability executes with VaultDON returning secret error", func(t *testing.T) {
		mockEnclaveClient := &MockEnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse]{}

		// --- Mock VaultDON Capability that returns a secret error ---
		mockVaultDONCapability := &MockVaultDONCapability{}
		mockVaultDONCapability.ExecuteFunc = func(ctx context.Context, req capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
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

		mockVaultDON := action.VaultDON{
			MasterPublicKey:       []byte{0xDE, 0xAD, 0xBE, 0xEF},
			CryptographyThreshold: 1,
			PossibleFaultyNodes:   0,
			ID:                    []uint8{0xF0, 0x0B, 0xAA, 0x42},
			Capability:            mockVaultDONCapability,
		}

		_, err := setupAndExecuteAction(t, mockEnclaveClient, mockVaultDON)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get encrypted decryption key shares from VaultDON: VaultDON returned an error for secret my-secret-id: secret not found")
	})

	t.Run("capability executes with VaultDON returning less number of shares than the threshold value", func(t *testing.T) {
		mockEnclaveClient := &MockEnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse]{}

		mockEnclaveClient.ExecuteBatchFunc = func(ctx context.Context, reqs []enclavetypes.SignedComputeRequest, enclaveIDs [][32]byte) ([]enclavetypes.RawExecuteResponse, error) {
			return mockEnclaveClient.commonExecuteBatchReturn(t)
		}

		// --- Mock VaultDON Capability that returns a secret error ---
		mockVaultDONCapability := &MockVaultDONCapability{}
		mockVaultDONCapability.ExecuteFunc = func(ctx context.Context, req capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
			vaultDONResponsePayload := &vault.GetSecretsResponse{
				Responses: []*vault.SecretResponse{
					{
						Id: &vault.SecretIdentifier{
							Key:       "my-secret-api-key",
							Namespace: "my-namespace",
							Owner:     "",
						},
						Result: &vault.SecretResponse_Data{
							Data: &vault.SecretData{
								EncryptedValue: "encrypted_secret_data_for_my-secret-id",
								EncryptedDecryptionKeyShares: []*vault.EncryptedShares{
									{
										Shares:        []string{hex.EncodeToString([]byte("share1_for_my-secret-id")), hex.EncodeToString([]byte("share2_for_my-secret-id"))},
										EncryptionKey: string([]byte("mock_public_key_bytes_1")),
									},
								},
							},
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

		mockVaultDON := action.VaultDON{
			MasterPublicKey: []byte{0xDE, 0xAD, 0xBE, 0xEF},
			// Set cryptographyThreshold to 2, and 1 possible faulty node. So, in this case, we need at least 3 shares
			CryptographyThreshold: 2,
			PossibleFaultyNodes:   1,
			ID:                    []uint8{0xF0, 0x0B, 0xAA, 0x42},
			Capability:            mockVaultDONCapability,
		}

		_, err := setupAndExecuteAction(t, mockEnclaveClient, mockVaultDON)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get encrypted decryption key shares from VaultDON: not enough encrypted decryption key shares for secret my-secret-api-key, expected at least 3, got 2")
	})
}
