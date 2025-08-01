package signertest

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/nacl/box"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/actions/vault"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/actions/vault/mock"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	attval "github.com/smartcontractkit/confidential-compute/enclave-client/attestation-validator"
	"github.com/smartcontractkit/confidential-compute/types"
	"github.com/smartcontractkit/confidential-compute/util"
	"github.com/smartcontractkit/tdh2/go/tdh2/tdh2easy"
)

const defaultTickInterval = 12 * time.Second

func setupTestDon(ctx context.Context, t *testing.T, lggr logger.Logger,
	workflowDonInfo framework.DonConfiguration, triggerSink framework.TriggerFactory, targetSink framework.TargetFactory, actionPath string) (workflowDon *framework.DON) {
	donContext := framework.CreateDonContext(ctx, t)

	_, publicKey, privateShares, err := tdh2easy.GenerateKeys(4, 4)
	require.NoError(t, err)

	// Start the minimal test server
	testServer := NewMinimalTestServer(publicKey)
	testServer.Start()
	t.Cleanup(func() {
		if err := testServer.Stop(); err != nil {
			t.Logf("Error stopping test server: %v", err)
		}
	})

	workflowDon = createTestWorkflowDon(ctx, t, lggr, workflowDonInfo, donContext, triggerSink, targetSink)

	// Load up the vault DON with a single secret.
	mySecret := []byte("peekaboo")
	ciphertext, err := tdh2easy.Encrypt(publicKey, mySecret)
	require.NoError(t, err)
	workflowDon.AddTargetCapability(NewVaultAction(privateShares, publicKey, map[string]*tdh2easy.Ciphertext{
		"my_secret": ciphertext,
	}))

	pcrs := attval.NitroPCRs{
		PCR0: []byte{},
		PCR1: []byte{},
		PCR2: []byte{},
	}
	encodedMeasurements, err := json.Marshal(pcrs)
	if err != nil {
		t.Fatalf("failed to encode PCRs: %v", err)
	}

	c2 := fmt.Sprintf("'{\"VaultDONID\":\"%s\",\"Enclaves\":[{\"ID\":\"%s\",\"URL\":\"%s\",\"TrustedValues\":\"%s\",\"EnclaveType\":\"%s\",\"ExtraData\":\"%s\"}]}'",
		util.EncodeToString([]byte(fmt.Sprintf("%d", workflowDonInfo.ID))), util.EncodeToString([]byte("123")), "http://localhost:8081", util.EncodeToString(encodedMeasurements), types.EnclaveTypeNitro, util.EncodeToString([]byte("")))

	workflowDon.AddStandardCapability("confidential-http-action", actionPath, c2)

	workflowDon.Initialise()

	servicetest.Run(t, workflowDon)

	donContext.WaitForCapabilitiesToBeExposed(t, workflowDon)

	workflowJob := GetWorkflowJob(t, workflowName, workflowOwnerID)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(defaultTickInterval)
		err := workflowDon.AddJob(ctx, &workflowJob)
		require.NoError(t, err)
	}()
	wg.Wait()
	return workflowDon
}

func createTestWorkflowDon(ctx context.Context, t *testing.T, lggr logger.Logger,
	workflowDonInfo framework.DonConfiguration,
	donContext framework.DonContext,
	triggerFactory framework.TriggerFactory,
	targetFactory framework.TargetFactory) *framework.DON {
	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonInfo,
		[]commoncap.DON{},
		donContext, true, 1*time.Second)

	workflowDon.AddTriggerCapability(triggerFactory)
	workflowDon.AddOCR3NonStandardCapability()
	workflowDon.AddTargetCapability(targetFactory)

	return workflowDon
}

var (
	_ commoncap.ExecutableCapability = &mock.Vault{}
)

type VaultActionFactory struct {
	services.StateMachine
	actionID      string
	targetName    string
	version       string
	privateShares []*tdh2easy.PrivateShare
	publicKey     *tdh2easy.PublicKey
	ciphertexts   map[string]*tdh2easy.Ciphertext

	actions []mock.Vault
}

func NewVaultAction(privateShares []*tdh2easy.PrivateShare, publicKey *tdh2easy.PublicKey, ciphertexts map[string]*tdh2easy.Ciphertext) *VaultActionFactory {
	return &VaultActionFactory{
		actionID:      vault.CapabilityID,
		targetName:    strings.Split(vault.CapabilityID, "@")[0],
		version:       strings.Split(vault.CapabilityID, "@")[1],
		privateShares: privateShares,
		publicKey:     publicKey,
		ciphertexts:   ciphertexts,
	}
}

func (ts *VaultActionFactory) GetTargetVersion() string {
	return ts.version
}

func (ts *VaultActionFactory) GetTargetName() string {
	return ts.targetName
}

func (ts *VaultActionFactory) GetTargetID() string {
	return ts.actionID
}

func (ts *VaultActionFactory) Start(ctx context.Context) error {
	return ts.StartOnce("VaultActionFactoryService", func() error {
		return nil
	})
}

func (ts *VaultActionFactory) Close() error {
	return ts.StopOnce("VaultActionFactoryService", func() error {
		return nil
	})
}

func (ts *VaultActionFactory) CreateNewTarget(t *testing.T) commoncap.ExecutableCapability {
	target := mock.Vault{
		Fn: func(ctx context.Context, req *vault.GetSecretsRequest) (*vault.GetSecretsResponse, error) {
			if req == nil {
				return nil, errors.New("request cannot be nil")
			}

			var resp []*vault.SecretResponse
			for _, req := range req.Requests {
				ciphertext, ok := ts.ciphertexts[req.Id.Key]
				if !ok {
					return nil, fmt.Errorf("secret ID %s not found", req.Id.Key)
				}
				ciphertextBytes, err := ciphertext.Marshal()
				if err != nil {
					return nil, fmt.Errorf("failed to marshal ciphertext for secret ID %s: %w", req.Id.Key, err)
				}
				data := vault.SecretData{
					EncryptedValue: util.EncodeToString(ciphertextBytes),
				}
				for _, pubKey := range req.EncryptionKeys {
					var edksArr []string
					for _, privateShare := range ts.privateShares {
						pubKeyBytes, err := util.DecodeString(pubKey)
						if err != nil {
							return nil, fmt.Errorf("failed to decode public key: %w", err)
						}
						if len(pubKeyBytes) != 32 {
							return nil, fmt.Errorf("invalid public key length: %d", len(pubKeyBytes))
						}
						pubKeyBytes32 := [32]byte(pubKeyBytes)
						dks, err := tdh2easy.Decrypt(ciphertext, privateShare)
						if err != nil {
							return nil, fmt.Errorf("failed to decrypt ciphertext for secret ID %s: %w", req.Id.Key, err)
						}
						dksBytes, err := dks.Marshal()
						if err != nil {
							return nil, fmt.Errorf("failed to marshal decryption share for secret ID %s: %w", req.Id.Key, err)
						}
						edks, err := box.SealAnonymous(nil, dksBytes, &pubKeyBytes32, nil)
						if err != nil {
							return nil, fmt.Errorf("failed to seal anonymous for secret ID %s: %w", req.Id.Key, err)
						}
						edksArr = append(edksArr, util.EncodeToString(edks))
					}
					data.EncryptedDecryptionKeyShares = append(
						data.EncryptedDecryptionKeyShares,
						&vault.EncryptedShares{Shares: edksArr, EncryptionKey: pubKey},
					)
					resp = append(resp, &vault.SecretResponse{
						Result: &vault.SecretResponse_Data{
							Data: &data,
						},
					})
				}
			}

			return &vault.GetSecretsResponse{
				Responses: resp,
			}, nil
		},
	}
	ts.actions = append(ts.actions, target)
	return &target
}

// MinimalTestServer provides a simple mock enclave server for testing
type MinimalTestServer struct {
	publicKey       [32]byte
	privateKey      [32]byte
	server          *http.Server
	masterPublicKey *tdh2easy.PublicKey
}

// NewMinimalTestServer creates a new minimal test server
func NewMinimalTestServer(masterPublicKey *tdh2easy.PublicKey) *MinimalTestServer {
	publicKey, privateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		panic(fmt.Sprintf("failed to generate keypair: %v", err))
	}

	return &MinimalTestServer{
		publicKey:       *publicKey,
		privateKey:      *privateKey,
		masterPublicKey: masterPublicKey,
	}
}

// Start starts the test server on port 8081
func (s *MinimalTestServer) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/publicKeys", s.handlePublicKeys)
	mux.HandleFunc("/requests", s.handleRequests)

	s.server = &http.Server{
		Addr:    ":8081",
		Handler: mux,
	}

	go func() {
		log.Printf("Starting minimal test server on :8081")
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Server error: %v", err)
		}
	}()

	// Give the server a moment to start
	time.Sleep(100 * time.Millisecond)
}

// Stop stops the test server
func (s *MinimalTestServer) Stop() error {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.server.Shutdown(ctx)
	}
	return nil
}

func (s *MinimalTestServer) handlePublicKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	config := types.EnclaveConfig{
		Signers:         [][]byte{},
		MasterPublicKey: []byte{},
		F:               1,
		T:               1,
	}
	response := types.PublicKeyResponse{
		PublicKeys:    [][]byte{s.publicKey[:]},
		CreationTimes: []time.Time{time.Now()},
		TTLs:          []time.Duration{24 * time.Hour},
		Config:        config,
		Attestation:   []byte("fake-attestation"),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding response: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// handleRequests handles the /requests endpoint
func (s *MinimalTestServer) handleRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read and parse the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	// Parse the request
	var req types.SignedComputeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		log.Printf("Error parsing request JSON: %v", err)
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	var msgs []string
	for i, ciphertext := range req.Ciphertexts {
		var ctxt tdh2easy.Ciphertext
		err := ctxt.UnmarshalVerify(ciphertext, s.masterPublicKey)
		if err != nil {
			log.Printf("Error unmarshalling ciphertext: %v", err)
			http.Error(w, "invalid ciphertext", http.StatusBadRequest)
			return
		}
		var pubKey [32]byte = s.publicKey
		var privKey [32]byte = s.privateKey
		var dksArr []*tdh2easy.DecryptionShare
		for _, edks := range req.EncryptedDecryptionKeyShares[i] {
			dksBytes, ok := box.OpenAnonymous(nil, edks, &pubKey, &privKey)
			if !ok {
				log.Printf("Error opening anonymous box")
				http.Error(w, "invalid encrypted decryption key share", http.StatusBadRequest)
				return
			}
			var dks tdh2easy.DecryptionShare
			err = dks.Unmarshal(dksBytes)
			if err != nil {
				log.Printf("Error unmarshalling decryption share: %v", err)
				http.Error(w, "invalid decryption share", http.StatusBadRequest)
				return
			}
			err := tdh2easy.VerifyShare(&ctxt, s.masterPublicKey, &dks)
			if err != nil {
				log.Printf("Error verifying decryption share: %v", err)
				http.Error(w, "invalid decryption share", http.StatusBadRequest)
				return
			}
			dksArr = append(dksArr, &dks)
		}

		msg, err := tdh2easy.Aggregate(&ctxt, dksArr, len(req.EncryptedDecryptionKeyShares[i]))
		if err != nil {
			log.Printf("Error aggregating decryption shares: %v", err)
			http.Error(w, "invalid decryption shares", http.StatusBadRequest)
			return
		}
		msgs = append(msgs, string(msg))
	}
	log.Printf("Processed %d messages", len(msgs))
	for i, msg := range msgs {
		log.Printf("Message %d: %s", i+1, msg)
	}

	msgsString := strings.Join(msgs, ",")
	msgsBytes, err := json.Marshal(msgsString)
	if err != nil {
		log.Printf("Error marshalling response: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := []types.HTTPResponse{{
		StatusCode: http.StatusOK,
		Body:       msgsBytes,
	}}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		log.Printf("Error marshalling response: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	config := types.EnclaveConfig{
		Signers:         [][]byte{},
		MasterPublicKey: []byte{},
		F:               1,
		T:               1,
	}
	rawResp := types.RawExecuteResponse{
		RequestID: req.RequestID,
		Config:    config,
		Output:    respBytes,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(rawResp); err != nil {
		log.Printf("Error encoding response: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}
