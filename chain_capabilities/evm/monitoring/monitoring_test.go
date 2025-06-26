package monitoring_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	capmonitoring "github.com/smartcontractkit/capabilities/monitoring"
)

type mockEmitter struct {
	mock.Mock
}

func (m *mockEmitter) Emit(_ context.Context, _ proto.Message, _ ...any) error {
	panic("implement me")
}

func (m *mockEmitter) EmitWithLog(ctx context.Context, msg proto.Message, _ ...any) error {
	args := m.Called(ctx, msg)
	return args.Error(0)
}

func TestProcessor_Process_InitiatedMessages(t *testing.T) {
	ctx := context.Background()
	initiated := []struct {
		name string
		msg  proto.Message
	}{
		{"CallContractInitiated", &monitoring.CallContractInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"FilterLogsInitiated", &monitoring.FilterLogsInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"BalanceAtInitiated", &monitoring.BalanceAtInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"EstimateGasInitiated", &monitoring.EstimateGasInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionByHashInitiated", &monitoring.GetTransactionByHashInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionReceiptInitiated", &monitoring.GetTransactionReceiptInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"LatestAndFinalizedHeadInitiated", &monitoring.LatestAndFinalizedHeadInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
	}

	for _, tc := range initiated {
		t.Run(tc.name, func(t *testing.T) {
			me := &mockEmitter{}
			me.On("EmitWithLog", ctx, tc.msg).Return(nil).Once()

			metrics, merr := monitoring.NewMetrics()
			require.NoError(t, merr)

			p, err := monitoring.NewProcessor(me, metrics)
			require.NoError(t, err)

			require.NoError(t, p.Process(ctx, tc.msg))
			me.AssertExpectations(t)
		})
	}
}

func TestProcessor_Process_InitiatedMessages_Error(t *testing.T) {
	ctx := context.Background()
	errIn := assert.AnError

	cases := []struct {
		name string
		msg  proto.Message
	}{
		{"CallContractInitiated", &monitoring.CallContractInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"FilterLogsInitiated", &monitoring.FilterLogsInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"BalanceAtInitiated", &monitoring.BalanceAtInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"EstimateGasInitiated", &monitoring.EstimateGasInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionByHashInitiated", &monitoring.GetTransactionByHashInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionReceiptInitiated", &monitoring.GetTransactionReceiptInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"LatestAndFinalizedHeadInitiated", &monitoring.LatestAndFinalizedHeadInitiated{ExecutionContext: &capmonitoring.ExecutionContext{}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			me := &mockEmitter{}
			me.On("EmitWithLog", ctx, tc.msg).Return(errIn).Once()

			metrics, merr := monitoring.NewMetrics()
			require.NoError(t, merr)

			p, err := monitoring.NewProcessor(me, metrics)
			require.NoError(t, err)

			procErr := p.Process(ctx, tc.msg)
			require.Error(t, procErr)
			assert.Contains(t, procErr.Error(), fmt.Sprintf("failed to emit %s log", tc.name))

			me.AssertExpectations(t)
		})
	}
}

type dummyProto struct{}

func (d *dummyProto) ProtoReflect() protoreflect.Message {
	return nil
}

func TestProcessor_Process_UnknownMessage_Noop(t *testing.T) {
	me := &mockEmitter{} // emitter never used
	metrics, merr := monitoring.NewMetrics()
	require.NoError(t, merr)

	p, err := monitoring.NewProcessor(me, metrics)
	require.NoError(t, err)

	err = p.Process(context.Background(), &dummyProto{})
	require.NoError(t, err)
}

func TestProcessor_Process_SuccessMessages(t *testing.T) {
	ctx := context.Background()
	successMsgs := []struct {
		name string
		msg  proto.Message
	}{
		{"CallContractSuccess", &monitoring.CallContractSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"FilterLogsSuccess", &monitoring.FilterLogsSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"BalanceAtSuccess", &monitoring.BalanceAtSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"EstimateGasSuccess", &monitoring.EstimateGasSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionByHashSuccess", &monitoring.GetTransactionByHashSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionReceiptSuccess", &monitoring.GetTransactionReceiptSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"LatestAndFinalizedHeadSuccess", &monitoring.LatestAndFinalizedHeadSuccess{ExecutionContext: &capmonitoring.ExecutionContext{}}},
	}

	for _, tc := range successMsgs {
		t.Run(tc.name, func(t *testing.T) {
			me := &mockEmitter{}
			me.On("EmitWithLog", ctx, tc.msg).Return(nil).Once()

			metrics, merr := monitoring.NewMetrics()
			require.NoError(t, merr)

			p, err := monitoring.NewProcessor(me, metrics)
			require.NoError(t, err)

			require.NoError(t, p.Process(ctx, tc.msg))
			me.AssertExpectations(t)
		})
	}
}

func TestProcessor_Process_ErrorMessages(t *testing.T) {
	ctx := context.Background()
	errorMsgs := []struct {
		name string
		msg  proto.Message
	}{
		{"CallContractError", &monitoring.CallContractError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"FilterLogsError", &monitoring.FilterLogsError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"BalanceAtError", &monitoring.BalanceAtError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"EstimateGasError", &monitoring.EstimateGasError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionByHashError", &monitoring.GetTransactionByHashError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"GetTransactionReceiptError", &monitoring.GetTransactionReceiptError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
		{"LatestAndFinalizedHeadError", &monitoring.LatestAndFinalizedHeadError{ExecutionContext: &capmonitoring.ExecutionContext{}}},
	}

	for _, tc := range errorMsgs {
		t.Run(tc.name, func(t *testing.T) {
			me := &mockEmitter{}
			me.On("EmitWithLog", ctx, tc.msg).Return(nil).Once()

			metrics, merr := monitoring.NewMetrics()
			require.NoError(t, merr)

			p, err := monitoring.NewProcessor(me, metrics)
			require.NoError(t, err)

			require.NoError(t, p.Process(ctx, tc.msg))
			me.AssertExpectations(t)
		})
	}
}
