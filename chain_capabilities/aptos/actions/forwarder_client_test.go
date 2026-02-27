package actions

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	"github.com/stretchr/testify/require"
)

func TestDecodeAccountUserTransaction_RawAPIShape(t *testing.T) {
	raw := []byte(`{
		"type":"user_transaction",
		"sequence_number":"42",
		"payload":{"function":"0x1::forwarder::report"},
		"events":[{"type":"0x1::forwarder::ReportProcessed","data":{"receiver":"0x1"}}]
	}`)

	decoded, err := decodeAccountUserTransaction(raw)
	require.NoError(t, err)
	require.Equal(t, uint64(42), decoded.SequenceNumber)
	require.Equal(t, "0x1::forwarder::report", decoded.EntryFunction)
	require.Len(t, decoded.Events, 1)
}

func TestDecodeAccountUserTransaction_SDKMarshaledShape(t *testing.T) {
	raw := []byte(`{
		"Type":"user_transaction",
		"Inner":{
			"SequenceNumber":42,
			"Payload":{
				"Type":"entry_function_payload",
				"Inner":{"Function":"0x1::forwarder::report"}
			},
			"Events":[{"Type":"0x1::forwarder::ReportProcessed","Data":{"receiver":"0x1"}}]
		}
	}`)

	decoded, err := decodeAccountUserTransaction(raw)
	require.NoError(t, err)
	require.Equal(t, uint64(42), decoded.SequenceNumber)
	require.Equal(t, "0x1::forwarder::report", decoded.EntryFunction)
	require.Len(t, decoded.Events, 1)
}

func TestIsMatchingReportProcessedData_WithJsonNumberAndCamelKeys(t *testing.T) {
	var receiver TransmissionID
	copy(receiver.Receiver[:], []byte{
		0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
		0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00,
		0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80,
		0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0, 0x01,
	})
	receiver.ReportID = [2]byte{0x01, 0x02}
	copy(receiver.WorkflowExecutionID[:], []byte{
		0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x11, 0x22,
		0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0x00,
		0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80,
		0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0, 0x01,
	})

	data := map[string]any{
		"Receiver":            "0x112233445566778899aabbccddeeff00102030405060708090a0b0c0d0e0f001",
		"reportId":            float64(258), // 0x0102
		"workflowExecutionId": "0xaabbccddeeff11223344556677889900102030405060708090a0b0c0d0e0f001",
	}

	require.True(t, isMatchingReportProcessedData(data, receiver))
}

func TestGetTransmissionTxHash_SelectsNewestExactMatch(t *testing.T) {
	transmissionID := newTestTransmissionID()
	forwarderAddress := newTestAddress(0x42)
	transmitter := accountAddressStringLong(newTestAddress(0x99))
	entryFunction := forwarderEntryFunction(forwarderAddress)

	mockService := &fakeAptosService{
		accountTransactionsReplies: []*aptostypes.AccountTransactionsReply{
			{
				Transactions: []*aptostypes.Transaction{
					mustUserTransaction(
						t,
						"0xlatest",
						10,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(mismatchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustUserTransaction(
						t,
						"0xold-match",
						2,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(matchingReportProcessedData(transmissionID)),
						},
					),
					mustUserTransaction(
						t,
						"0xnew-match",
						9,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(matchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustUserTransaction(
						t,
						"0xlatest",
						10,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(mismatchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
		},
	}

	client := &forwarderClient{
		AptosService:     mockService,
		forwarderAddress: forwarderAddress,
	}

	hash, err := client.GetTransmissionTxHash(context.Background(), transmissionID, transmitter, nil)
	require.NoError(t, err)
	require.Equal(t, "0xnew-match", hash)

	require.Len(t, mockService.accountTransactionsCalls, 2)
	require.Nil(t, mockService.accountTransactionsCalls[0].Start)
	require.NotNil(t, mockService.accountTransactionsCalls[0].Limit)
	require.Equal(t, uint64(1), *mockService.accountTransactionsCalls[0].Limit)
	require.NotNil(t, mockService.accountTransactionsCalls[1].Start)
	require.Equal(t, uint64(0), *mockService.accountTransactionsCalls[1].Start)
}

func TestGetTransmissionTxHash_FallsBackToNewestReportProcessedWhenNoExactMatch(t *testing.T) {
	transmissionID := newTestTransmissionID()
	forwarderAddress := newTestAddress(0x43)
	transmitter := accountAddressStringLong(newTestAddress(0x98))
	entryFunction := forwarderEntryFunction(forwarderAddress)

	mockService := &fakeAptosService{
		accountTransactionsReplies: []*aptostypes.AccountTransactionsReply{
			{
				Transactions: []*aptostypes.Transaction{
					mustUserTransaction(
						t,
						"0xlatest",
						8,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(mismatchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustUserTransaction(
						t,
						"0xfallback-old",
						3,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(mismatchingReportProcessedData(transmissionID)),
						},
					),
					mustUserTransaction(
						t,
						"0xfallback-new",
						8,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(mismatchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustUserTransaction(
						t,
						"0xlatest",
						8,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(mismatchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
		},
	}

	client := &forwarderClient{
		AptosService:     mockService,
		forwarderAddress: forwarderAddress,
	}

	hash, err := client.GetTransmissionTxHash(context.Background(), transmissionID, transmitter, nil)
	require.NoError(t, err)
	require.Equal(t, "0xfallback-new", hash)
}

func TestGetTransmissionTxHash_TopUpPassFindsNewTransaction(t *testing.T) {
	transmissionID := newTestTransmissionID()
	forwarderAddress := newTestAddress(0x44)
	transmitter := accountAddressStringLong(newTestAddress(0x97))
	entryFunction := forwarderEntryFunction(forwarderAddress)

	mockService := &fakeAptosService{
		accountTransactionsReplies: []*aptostypes.AccountTransactionsReply{
			{
				Transactions: []*aptostypes.Transaction{
					mustUserTransaction(
						t,
						"0xlatest-initial",
						50,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(mismatchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustUserTransaction(
						t,
						"0xpage1",
						50,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(mismatchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustUserTransaction(
						t,
						"0xpage2",
						1,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(mismatchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustUserTransaction(
						t,
						"0xlatest-after",
						52,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(mismatchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustUserTransaction(
						t,
						"0xtopup-match",
						52,
						entryFunction,
						true,
						[]map[string]any{
							reportProcessedEvent(matchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
		},
	}

	client := &forwarderClient{
		AptosService:     mockService,
		forwarderAddress: forwarderAddress,
	}

	hash, err := client.GetTransmissionTxHash(context.Background(), transmissionID, transmitter, nil)
	require.NoError(t, err)
	require.Equal(t, "0xtopup-match", hash)

	require.Len(t, mockService.accountTransactionsCalls, 5)
	require.NotNil(t, mockService.accountTransactionsCalls[4].Start)
	require.NotNil(t, mockService.accountTransactionsCalls[4].Limit)
	require.Equal(t, uint64(51), *mockService.accountTransactionsCalls[4].Start)
	require.Equal(t, uint64(2), *mockService.accountTransactionsCalls[4].Limit)
}

func TestGetTransmissionTxHash_RejectsMismatchedExpectedPayload(t *testing.T) {
	transmissionID := newTestTransmissionID()
	otherTransmission := transmissionID
	otherTransmission.ReportID = [2]byte{0xaa, 0xbb}

	forwarderAddress := newTestAddress(0x49)
	transmitter := accountAddressStringLong(newTestAddress(0x94))
	entryFunction := forwarderEntryFunction(forwarderAddress)

	expectedRawReport := mustEncodedReportWithMetadata(t, transmissionID)
	mismatchedRawReport := mustEncodedReportWithMetadata(t, otherTransmission)

	mockService := &fakeAptosService{
		accountTransactionsReplies: []*aptostypes.AccountTransactionsReply{
			{
				Transactions: []*aptostypes.Transaction{
					mustSuccessfulUserTransactionWithPayload(
						t,
						"0xlatest",
						8,
						entryFunction,
						transmissionID.Receiver.StringLong(),
						mismatchedRawReport,
						[]map[string]any{
							reportProcessedEvent(matchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustSuccessfulUserTransactionWithPayload(
						t,
						"0xmismatch",
						8,
						entryFunction,
						transmissionID.Receiver.StringLong(),
						mismatchedRawReport,
						[]map[string]any{
							reportProcessedEvent(matchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustSuccessfulUserTransactionWithPayload(
						t,
						"0xlatest",
						8,
						entryFunction,
						transmissionID.Receiver.StringLong(),
						mismatchedRawReport,
						[]map[string]any{
							reportProcessedEvent(matchingReportProcessedData(transmissionID)),
						},
					),
				},
			},
		},
	}

	client := &forwarderClient{
		AptosService:     mockService,
		forwarderAddress: forwarderAddress,
	}

	_, err := client.GetTransmissionTxHash(context.Background(), transmissionID, transmitter, expectedRawReport)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no matching successful report tx found")
}

func TestGetTransmissionTxHash_AcceptsMatchingPayloadWithoutReportProcessedEvent(t *testing.T) {
	transmissionID := newTestTransmissionID()
	forwarderAddress := newTestAddress(0x4a)
	transmitter := accountAddressStringLong(newTestAddress(0x93))
	entryFunction := forwarderEntryFunction(forwarderAddress)

	expectedRawReport := mustEncodedReportWithMetadata(t, transmissionID)
	matchHash := "0x" + strings.Repeat("d", 64)
	latestHash := "0x" + strings.Repeat("e", 64)

	mockService := &fakeAptosService{
		accountTransactionsReplies: []*aptostypes.AccountTransactionsReply{
			{
				Transactions: []*aptostypes.Transaction{
					mustSuccessfulUserTransactionWithPayload(
						t,
						latestHash,
						9,
						entryFunction,
						transmissionID.Receiver.StringLong(),
						expectedRawReport,
						// No ReportProcessed event in account tx payload.
						[]map[string]any{},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustSuccessfulUserTransactionWithPayload(
						t,
						matchHash,
						9,
						entryFunction,
						transmissionID.Receiver.StringLong(),
						expectedRawReport,
						[]map[string]any{},
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustSuccessfulUserTransactionWithPayload(
						t,
						latestHash,
						9,
						entryFunction,
						transmissionID.Receiver.StringLong(),
						expectedRawReport,
						[]map[string]any{},
					),
				},
			},
		},
		transactionByHashReplies: []*aptostypes.TransactionByHashReply{
			{
				Transaction: mustSuccessfulUserTransactionWithPayload(
					t,
					matchHash,
					9,
					entryFunction,
					transmissionID.Receiver.StringLong(),
					expectedRawReport,
					[]map[string]any{},
				),
			},
		},
	}

	client := &forwarderClient{
		AptosService:     mockService,
		forwarderAddress: forwarderAddress,
	}

	hash, err := client.GetTransmissionTxHash(context.Background(), transmissionID, transmitter, expectedRawReport)
	require.NoError(t, err)
	require.Equal(t, matchHash, hash)
}

func TestGetTransmissionFailedTxHash_SelectsEarliestMatchingFailedAcrossTransmitters(t *testing.T) {
	transmissionID := newTestTransmissionID()
	forwarderAddress := newTestAddress(0x45)
	entryFunction := forwarderEntryFunction(forwarderAddress)
	transmitter1 := accountAddressStringLong(newTestAddress(0x96))
	transmitter2 := accountAddressStringLong(newTestAddress(0x95))

	otherTransmission := transmissionID
	otherTransmission.ReportID = [2]byte{0xaa, 0xbb}

	matchingRawReport := mustEncodedReportWithMetadata(t, transmissionID)
	mismatchingRawReport := mustEncodedReportWithMetadata(t, otherTransmission)

	mockService := &fakeAptosService{
		accountTransactionsReplies: []*aptostypes.AccountTransactionsReply{
			{
				Transactions: []*aptostypes.Transaction{
					mustFailedUserTransactionWithPayload(
						t,
						"0xlatest-t1",
						10,
						1010,
						250,
						entryFunction,
						transmissionID.Receiver.StringLong(),
						mismatchingRawReport,
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustFailedUserTransactionWithPayload(
						t,
						"0xlate-invalid",
						9,
						1009,
						200,
						entryFunction,
						transmissionID.Receiver.StringLong(),
						matchingRawReport,
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustFailedUserTransactionWithPayload(
						t,
						"0xlatest-t2",
						8,
						1008,
						180,
						entryFunction,
						transmissionID.Receiver.StringLong(),
						mismatchingRawReport,
					),
				},
			},
			{
				Transactions: []*aptostypes.Transaction{
					mustFailedUserTransactionWithPayload(
						t,
						"0xearliest-invalid",
						7,
						1007,
						100,
						entryFunction,
						transmissionID.Receiver.StringLong(),
						matchingRawReport,
					),
				},
			},
		},
	}

	client := &forwarderClient{
		AptosService:     mockService,
		forwarderAddress: forwarderAddress,
	}

	hash, err := client.GetTransmissionFailedTxHash(context.Background(), transmissionID, []string{transmitter1, transmitter2})
	require.NoError(t, err)
	require.Equal(t, "0xearliest-invalid", hash)

	require.Len(t, mockService.accountTransactionsCalls, 4)
	require.Nil(t, mockService.accountTransactionsCalls[0].Start)
	require.NotNil(t, mockService.accountTransactionsCalls[0].Limit)
	require.NotNil(t, mockService.accountTransactionsCalls[1].Start)
	require.NotNil(t, mockService.accountTransactionsCalls[2].Limit)
	require.NotNil(t, mockService.accountTransactionsCalls[3].Start)
}

func TestValidateFailedTxHash_AcceptsMatchingFailedReceipt(t *testing.T) {
	transmissionID := newTestTransmissionID()
	forwarderAddress := newTestAddress(0x46)
	entryFunction := forwarderEntryFunction(forwarderAddress)
	matchingRawReport := mustEncodedReportWithMetadata(t, transmissionID)

	failedTx := mustFailedUserTransactionWithPayload(
		t,
		"0x"+strings.Repeat("a", 64),
		11,
		1111,
		2222,
		entryFunction,
		transmissionID.Receiver.StringLong(),
		matchingRawReport,
	)

	mockService := &fakeAptosService{
		transactionByHashReplies: []*aptostypes.TransactionByHashReply{
			{Transaction: failedTx},
		},
	}

	client := &forwarderClient{
		AptosService:     mockService,
		forwarderAddress: forwarderAddress,
	}

	hash, err := client.ValidateFailedTxHash(context.Background(), transmissionID, "0x"+strings.Repeat("a", 64), nil)
	require.NoError(t, err)
	require.Equal(t, "0x"+strings.Repeat("a", 64), hash)
	require.Len(t, mockService.transactionByHashCalls, 1)
	require.Equal(t, "0x"+strings.Repeat("a", 64), mockService.transactionByHashCalls[0].Hash)
}

func TestValidateFailedTxHash_RejectsSuccessfulReceipt(t *testing.T) {
	transmissionID := newTestTransmissionID()
	forwarderAddress := newTestAddress(0x47)
	entryFunction := forwarderEntryFunction(forwarderAddress)

	successTx := mustUserTransaction(
		t,
		"0x"+strings.Repeat("b", 64),
		1,
		entryFunction,
		true,
		[]map[string]any{},
	)
	version := uint64(1)
	successTx.Version = &version

	mockService := &fakeAptosService{
		transactionByHashReplies: []*aptostypes.TransactionByHashReply{
			{Transaction: successTx},
		},
	}

	client := &forwarderClient{
		AptosService:     mockService,
		forwarderAddress: forwarderAddress,
	}

	_, err := client.ValidateFailedTxHash(context.Background(), transmissionID, "0x"+strings.Repeat("b", 64), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected failure")
}

func TestValidateFailedTxHash_RejectsMismatchedExpectedReportPayload(t *testing.T) {
	transmissionID := newTestTransmissionID()
	forwarderAddress := newTestAddress(0x48)
	entryFunction := forwarderEntryFunction(forwarderAddress)
	matchingRawReport := mustEncodedReportWithMetadata(t, transmissionID)

	failedTx := mustFailedUserTransactionWithPayload(
		t,
		"0x"+strings.Repeat("c", 64),
		12,
		3333,
		4444,
		entryFunction,
		transmissionID.Receiver.StringLong(),
		matchingRawReport,
	)

	mockService := &fakeAptosService{
		transactionByHashReplies: []*aptostypes.TransactionByHashReply{
			{Transaction: failedTx},
		},
	}

	client := &forwarderClient{
		AptosService:     mockService,
		forwarderAddress: forwarderAddress,
	}

	// Same transmission metadata, but different report payload bytes.
	mismatchedExpected := append([]byte(nil), matchingRawReport...)
	mismatchedExpected[len(mismatchedExpected)-1] ^= 0x01

	_, err := client.ValidateFailedTxHash(context.Background(), transmissionID, "0x"+strings.Repeat("c", 64), mismatchedExpected)
	require.Error(t, err)
	require.Contains(t, err.Error(), "payload does not match requested transmission")
}

type fakeAptosService struct {
	accountTransactionsReplies []*aptostypes.AccountTransactionsReply
	accountTransactionsCalls   []aptostypes.AccountTransactionsRequest
	accountTransactionsIndex   int
	transactionByHashReplies   []*aptostypes.TransactionByHashReply
	transactionByHashCalls     []aptostypes.TransactionByHashRequest
	transactionByHashIndex     int
}

func (f *fakeAptosService) AccountAPTBalance(context.Context, aptostypes.AccountAPTBalanceRequest) (*aptostypes.AccountAPTBalanceReply, error) {
	return &aptostypes.AccountAPTBalanceReply{}, nil
}

func (f *fakeAptosService) LedgerVersion(context.Context) (uint64, error) {
	return 0, nil
}

func (f *fakeAptosService) View(context.Context, aptostypes.ViewRequest) (*aptostypes.ViewReply, error) {
	return &aptostypes.ViewReply{}, nil
}

func (f *fakeAptosService) EventsByHandle(context.Context, aptostypes.EventsByHandleRequest) (*aptostypes.EventsByHandleReply, error) {
	return &aptostypes.EventsByHandleReply{}, nil
}

func (f *fakeAptosService) TransactionByHash(_ context.Context, req aptostypes.TransactionByHashRequest) (*aptostypes.TransactionByHashReply, error) {
	f.transactionByHashCalls = append(f.transactionByHashCalls, req)
	index := f.transactionByHashIndex
	f.transactionByHashIndex++
	if index >= len(f.transactionByHashReplies) {
		return &aptostypes.TransactionByHashReply{}, nil
	}
	reply := f.transactionByHashReplies[index]
	if reply == nil {
		return &aptostypes.TransactionByHashReply{}, nil
	}
	return reply, nil
}

func (f *fakeAptosService) SubmitTransaction(context.Context, aptostypes.SubmitTransactionRequest) (*aptostypes.SubmitTransactionReply, error) {
	return &aptostypes.SubmitTransactionReply{}, nil
}

func (f *fakeAptosService) AccountTransactions(_ context.Context, req aptostypes.AccountTransactionsRequest) (*aptostypes.AccountTransactionsReply, error) {
	f.accountTransactionsCalls = append(f.accountTransactionsCalls, req)
	index := f.accountTransactionsIndex
	f.accountTransactionsIndex++
	if index >= len(f.accountTransactionsReplies) {
		return &aptostypes.AccountTransactionsReply{}, nil
	}
	reply := f.accountTransactionsReplies[index]
	if reply == nil {
		return &aptostypes.AccountTransactionsReply{}, nil
	}
	return reply, nil
}

func newTestTransmissionID() TransmissionID {
	var transmissionID TransmissionID
	for i := range transmissionID.Receiver {
		transmissionID.Receiver[i] = byte(i + 1)
	}
	transmissionID.ReportID = [2]byte{0x12, 0x34}
	for i := range transmissionID.WorkflowExecutionID {
		transmissionID.WorkflowExecutionID[i] = byte(255 - i)
	}
	return transmissionID
}

func newTestAddress(seed byte) [32]byte {
	var address [32]byte
	for i := range address {
		address[i] = seed
	}
	address[len(address)-1] = seed + 1
	return address
}

func forwarderEntryFunction(forwarderAddress [32]byte) string {
	address := aptos_sdk.AccountAddress(forwarderAddress)
	return fmt.Sprintf("%s::forwarder::report", address.StringLong())
}

func accountAddressStringLong(addressBytes [32]byte) string {
	address := aptos_sdk.AccountAddress(addressBytes)
	return address.StringLong()
}

func matchingReportProcessedData(transmissionID TransmissionID) map[string]any {
	return map[string]any{
		"receiver":              transmissionID.Receiver.StringLong(),
		"report_id":             strconv.FormatUint(uint64(binary.BigEndian.Uint16(transmissionID.ReportID[:])), 10),
		"workflow_execution_id": "0x" + hex.EncodeToString(transmissionID.WorkflowExecutionID[:]),
	}
}

func mismatchingReportProcessedData(transmissionID TransmissionID) map[string]any {
	data := matchingReportProcessedData(transmissionID)
	data["report_id"] = strconv.FormatUint(uint64(binary.BigEndian.Uint16(transmissionID.ReportID[:]))+1, 10)
	return data
}

func reportProcessedEvent(data map[string]any) map[string]any {
	return map[string]any{
		"type": "0x1::forwarder::ReportProcessed",
		"data": data,
	}
}

func mustUserTransaction(
	t *testing.T,
	hash string,
	sequenceNumber uint64,
	entryFunction string,
	success bool,
	events []map[string]any,
) *aptostypes.Transaction {
	t.Helper()

	if events == nil {
		events = []map[string]any{}
	}

	raw, err := json.Marshal(map[string]any{
		"type":            "user_transaction",
		"sequence_number": strconv.FormatUint(sequenceNumber, 10),
		"payload": map[string]any{
			"function": entryFunction,
		},
		"events": events,
	})
	require.NoError(t, err)

	txSuccess := success
	return &aptostypes.Transaction{
		Type:    aptostypes.TransactionVariantUser,
		Hash:    hash,
		Success: &txSuccess,
		Data:    raw,
	}
}

func mustFailedUserTransactionWithPayload(
	t *testing.T,
	hash string,
	sequenceNumber uint64,
	version uint64,
	timestampMicros uint64,
	entryFunction string,
	receiver string,
	rawReport []byte,
) *aptostypes.Transaction {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"type":            "user_transaction",
		"sequence_number": strconv.FormatUint(sequenceNumber, 10),
		"version":         strconv.FormatUint(version, 10),
		"timestamp":       strconv.FormatUint(timestampMicros, 10),
		"payload": map[string]any{
			"function":  entryFunction,
			"arguments": []any{receiver, "0x" + hex.EncodeToString(rawReport), []any{}},
		},
		"events": []any{},
	})
	require.NoError(t, err)

	txSuccess := false
	txVersion := version
	return &aptostypes.Transaction{
		Type:    aptostypes.TransactionVariantUser,
		Hash:    hash,
		Version: &txVersion,
		Success: &txSuccess,
		Data:    raw,
	}
}

func mustSuccessfulUserTransactionWithPayload(
	t *testing.T,
	hash string,
	sequenceNumber uint64,
	entryFunction string,
	receiver string,
	rawReport []byte,
	events []map[string]any,
) *aptostypes.Transaction {
	t.Helper()

	if events == nil {
		events = []map[string]any{}
	}

	raw, err := json.Marshal(map[string]any{
		"type":            "user_transaction",
		"sequence_number": strconv.FormatUint(sequenceNumber, 10),
		"payload": map[string]any{
			"function":  entryFunction,
			"arguments": []any{receiver, "0x" + hex.EncodeToString(rawReport), []any{}},
		},
		"events": events,
	})
	require.NoError(t, err)

	txSuccess := true
	return &aptostypes.Transaction{
		Type:    aptostypes.TransactionVariantUser,
		Hash:    hash,
		Success: &txSuccess,
		Data:    raw,
	}
}

func mustEncodedReportWithMetadata(t *testing.T, transmissionID TransmissionID) []byte {
	t.Helper()

	metadata := ocrtypes.Metadata{
		Version:          1,
		ExecutionID:      hex.EncodeToString(transmissionID.WorkflowExecutionID[:]),
		Timestamp:        1,
		DONID:            1,
		DONConfigVersion: 1,
		WorkflowID:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		WorkflowName:     "0102030405060708090a",
		WorkflowOwner:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ReportID:         hex.EncodeToString(transmissionID.ReportID[:]),
	}

	encoded, err := metadata.Encode()
	require.NoError(t, err)
	return append(encoded, 0xff)
}
