package actions

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
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

	hash, err := client.GetTransmissionTxHash(context.Background(), transmissionID, transmitter)
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

	hash, err := client.GetTransmissionTxHash(context.Background(), transmissionID, transmitter)
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

	hash, err := client.GetTransmissionTxHash(context.Background(), transmissionID, transmitter)
	require.NoError(t, err)
	require.Equal(t, "0xtopup-match", hash)

	require.Len(t, mockService.accountTransactionsCalls, 5)
	require.NotNil(t, mockService.accountTransactionsCalls[4].Start)
	require.NotNil(t, mockService.accountTransactionsCalls[4].Limit)
	require.Equal(t, uint64(51), *mockService.accountTransactionsCalls[4].Start)
	require.Equal(t, uint64(2), *mockService.accountTransactionsCalls[4].Limit)
}

type fakeAptosService struct {
	accountTransactionsReplies []*aptostypes.AccountTransactionsReply
	accountTransactionsCalls   []aptostypes.AccountTransactionsRequest
	accountTransactionsIndex   int
}

func (f *fakeAptosService) AccountAPTBalance(context.Context, aptostypes.AccountAPTBalanceRequest) (*aptostypes.AccountAPTBalanceReply, error) {
	return &aptostypes.AccountAPTBalanceReply{}, nil
}

func (f *fakeAptosService) View(context.Context, aptostypes.ViewRequest) (*aptostypes.ViewReply, error) {
	return &aptostypes.ViewReply{}, nil
}

func (f *fakeAptosService) EventsByHandle(context.Context, aptostypes.EventsByHandleRequest) (*aptostypes.EventsByHandleReply, error) {
	return &aptostypes.EventsByHandleReply{}, nil
}

func (f *fakeAptosService) TransactionByHash(context.Context, aptostypes.TransactionByHashRequest) (*aptostypes.TransactionByHashReply, error) {
	return &aptostypes.TransactionByHashReply{}, nil
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
