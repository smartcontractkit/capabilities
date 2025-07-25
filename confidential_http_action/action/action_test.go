package action_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
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
// It now also takes type parameters, just like the actual interface.
type MockEnclaveClient[PublicDataType any, OutputType any] struct {
	GetPublicKeysFunc func(ctx context.Context, requestID [32]byte, enclaveSpecifications []byte) ([]enclavetypes.EnclavePublicKeyData, error)

	// The ExecuteFunc's signature now correctly uses the struct's own type parameters
	ExecuteFunc func(ctx context.Context, requestID [32]byte, publicData PublicDataType, ciphertexts [][]byte, encryptedDecryptionKeyShares [][][]byte, enclaveEphemeralPublicKey []byte, enclaveID [32]byte) (*enclavetypes.ExecuteResponse[OutputType], error)

	ExecuteBatchFunc func(ctx context.Context, reqs []enclavetypes.SignedComputeRequest, enclaveIDs [][32]byte) ([]enclavetypes.RawExecuteResponse, error)

	UpdateNodesFunc func(nodes []enclavetypes.EnclaveNode)

	CapturedSignature []byte
}

// Implement methods with POINTER RECEIVERS, using the struct's type parameters
func (m *MockEnclaveClient[PublicDataType, OutputType]) GetPublicKeys(ctx context.Context, requestID [32]byte, enclaveSpecifications []byte) ([]enclavetypes.EnclavePublicKeyData, error) {
	if m.GetPublicKeysFunc != nil {
		return m.GetPublicKeysFunc(ctx, requestID, enclaveSpecifications)
	}
	return nil, nil
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

func TestNew(t *testing.T) {
	t.Run("a new confidential http capability action is created", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(logger.Test(t), getTestConfig(), mockKeystore)

		assert.NoError(t, err)
		assert.NotNil(t, c)
	})
}

func TestCapability_Info(t *testing.T) {
	t.Run("capability info is reported correctly", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(logger.Test(t), getTestConfig(), mockKeystore)
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
		mockKeystore := &mockKeystore{
			accounts: []string{core.P2PAccountKey},
			signFunc: func(ctx context.Context, account string, msg []byte) ([]byte, error) {
				return []byte("test-signature"), nil
			},
		}

		mockEnclaveClient := &MockEnclaveClient[httpenclavetypes.HTTPEnclaveRequestData, []enclavetypes.HTTPResponse]{}
		mockEnclaveClient.GetPublicKeysFunc = func(ctx context.Context, requestID [32]byte, enclaveSpecifications []byte) ([]enclavetypes.EnclavePublicKeyData, error) {
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

		mockResponsesSlice := []enclavetypes.HTTPResponse{
			{StatusCode: 200, Body: []byte("First response")},
			{StatusCode: 500, Body: []byte("Second response")},
		}
		innerJSONBytes, err := json.Marshal(mockResponsesSlice)
		assert.NoError(t, err)

		mockEnclaveClient.ExecuteBatchFunc = func(ctx context.Context, reqs []enclavetypes.SignedComputeRequest, enclaveIDs [][32]byte) ([]enclavetypes.RawExecuteResponse, error) {
			if len(reqs) == 1 {
				mockEnclaveClient.CapturedSignature = reqs[0].Signature
			}
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

		c, err := action.NewWithEnclaveClient(
			logger.Test(t),
			getTestConfig(),
			mockKeystore,
			mockEnclaveClient,
			[]byte{0xDE, 0xAD, 0xBE, 0xEF}, // vaultDONPublicKey
		)
		assert.NoError(t, err)

		input := cap.Input{
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
			VaultDonSecretIds: []string{},
		}

		inputsValue, err := values.WrapMap(input)
		assert.NoError(t, err)

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

		resp, err := c.Execute(context.Background(), workflow.NewRequest(map[string]any{
			"Inputs": inputsValue,
		}))
		assert.NoError(t, err)
		assert.NotNil(t, resp.Value)

		var capOutput cap.Output
		err = resp.Value.UnwrapTo(&capOutput)
		assert.NoError(t, err)

		assert.Equal(t, []byte("test-signature"), mockEnclaveClient.CapturedSignature, "Expected the captured signature to match the mock signature")

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
}
