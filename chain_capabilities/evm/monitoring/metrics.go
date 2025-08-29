package monitoring

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"go.opentelemetry.io/otel/attribute"

	commoncapbeholder "github.com/smartcontractkit/capabilities/libs/monitoring"

	commonbeholder "github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

func ns(name string) string { return fmt.Sprintf("evm_capability_%s", name) }

// Metrics holds all per-method instruments
type Metrics struct {
	CallContractSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	CallContractError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	WriteReportSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	WriteReportError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	LogTriggerSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	LogTriggerError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	LogTriggerCleanUpError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	LogTriggerEventDroppedError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	FilterLogsSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	FilterLogsError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	BalanceAtSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	BalanceAtError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	EstimateGasSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	EstimateGasError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	GetTxByHashSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	GetTxByHashError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	GetReceiptSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	GetReceiptError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	HeaderByNumberSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	HeaderByNumberError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
}

// NewMetrics constructs all counters & histograms bound to a given chainID
func NewMetrics() (Metrics, error) {
	m := Metrics{}
	var err error

	// -- CallContract --
	ccSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("call_contract_success"), commonbeholder.ToSchemaFullName(&CallContractSuccess{}))
	m.CallContractSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(ccSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create call contract success metric: %w", err)
	}
	ccErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("call_contract_error"), commonbeholder.ToSchemaFullName(&CallContractError{}))
	m.CallContractError.basic, err = commoncapbeholder.NewMetricsCapBasic(ccErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create call contract error metric: %w", err)
	}
	// -- WriteReport --
	wrSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("write_report_success"), commonbeholder.ToSchemaFullName(&WriteReportSuccess{}))
	m.WriteReportSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(wrSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report success metric: %w", err)
	}
	wrErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("write_report_error"), commonbeholder.ToSchemaFullName(&WriteReportError{}))
	m.WriteReportError.basic, err = commoncapbeholder.NewMetricsCapBasic(wrErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report error metric: %w", err)
	}

	// -- LogTrigger --
	ltSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("log_trigger_success"), commonbeholder.ToSchemaFullName(&LogTriggerSuccess{}))
	m.LogTriggerSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(ltSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create log trigger success metric: %w", err)
	}
	ltErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("log_trigger_error"), commonbeholder.ToSchemaFullName(&LogTriggerError{}))
	m.LogTriggerError.basic, err = commoncapbeholder.NewMetricsCapBasic(ltErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create log trigger error metric: %w", err)
	}
	ltcuErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("log_trigger_clean_up_error"), commonbeholder.ToSchemaFullName(&LogTriggerCleanUpError{}))
	m.LogTriggerCleanUpError.basic, err = commoncapbeholder.NewMetricsCapBasic(ltcuErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create log trigger clean up error metric: %w", err)
	}
	ltedErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("log_trigger_event_dropped_error"), commonbeholder.ToSchemaFullName(&LogTriggerEventDroppedError{}))
	m.LogTriggerEventDroppedError.basic, err = commoncapbeholder.NewMetricsCapBasic(ltedErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create log trigger event dropped error metric: %w", err)
	}

	// -- FilterLogs --
	flSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("filter_logs_success"), commonbeholder.ToSchemaFullName(&FilterLogsSuccess{}))
	m.FilterLogsSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(flSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create filter logs success metric: %w", err)
	}
	flErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("filter_logs_error"), commonbeholder.ToSchemaFullName(&FilterLogsError{}))
	m.FilterLogsError.basic, err = commoncapbeholder.NewMetricsCapBasic(flErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create filter logs error metric: %w", err)
	}

	// -- BalanceAt --
	baSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("balance_at_success"), commonbeholder.ToSchemaFullName(&BalanceAtSuccess{}))
	m.BalanceAtSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(baSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create balance at success metric: %w", err)
	}
	baErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("balance_at_error"), commonbeholder.ToSchemaFullName(&BalanceAtError{}))
	m.BalanceAtError.basic, err = commoncapbeholder.NewMetricsCapBasic(baErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create balance at error metric: %w", err)
	}

	// -- EstimateGas --
	egSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("estimate_gas_success"), commonbeholder.ToSchemaFullName(&EstimateGasSuccess{}))
	m.EstimateGasSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(egSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create estimate gas success metric: %w", err)
	}
	egErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("estimate_gas_error"), commonbeholder.ToSchemaFullName(&EstimateGasError{}))
	m.EstimateGasError.basic, err = commoncapbeholder.NewMetricsCapBasic(egErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create estimate gas error metric: %w", err)
	}

	// -- GetTransactionByHash --
	txSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("get_transaction_by_hash_success"), commonbeholder.ToSchemaFullName(&GetTransactionByHashSuccess{}))
	m.GetTxByHashSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(txSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create get tx by hash success metric: %w", err)
	}
	txErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("get_transaction_by_hash_error"), commonbeholder.ToSchemaFullName(&GetTransactionByHashError{}))
	m.GetTxByHashError.basic, err = commoncapbeholder.NewMetricsCapBasic(txErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create get tx by hash error metric: %w", err)
	}

	// -- GetTransactionReceipt --
	rcSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("get_transaction_receipt_success"), commonbeholder.ToSchemaFullName(&GetTransactionReceiptSuccess{}))
	m.GetReceiptSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(rcSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create get receipt success metric: %w", err)
	}
	rcErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("get_transaction_receipt_error"), commonbeholder.ToSchemaFullName(&GetTransactionReceiptError{}))
	m.GetReceiptError.basic, err = commoncapbeholder.NewMetricsCapBasic(rcErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create get receipt error metric: %w", err)
	}

	// -- HeaderByNumber --
	headerByNumberSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("header_by_number_success"), commonbeholder.ToSchemaFullName(&HeaderByNumberSuccess{}))
	m.HeaderByNumberSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(headerByNumberSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create header by number success metric: %w", err)
	}
	headerByNumberErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("header_by_number_error"), commonbeholder.ToSchemaFullName(&HeaderByNumberError{}))
	m.HeaderByNumberError.basic, err = commoncapbeholder.NewMetricsCapBasic(headerByNumberErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create header by number error metric: %w", err)
	}

	return m, nil
}

// -- CallContract --

func (m *Metrics) OnCallContractSuccess(ctx context.Context, msg *CallContractSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.CallContractSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnCallContractError(ctx context.Context, msg *CallContractError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.CallContractError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

// -- WriteReport --

func (m *Metrics) OnWriteReportSuccess(ctx context.Context, msg *WriteReportSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.WriteReportSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnWriteReportError(ctx context.Context, msg *WriteReportError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.WriteReportError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

// -- LogTrigger --

func (m *Metrics) OnLogTriggerSuccess(ctx context.Context, msg *LogTriggerSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.LogTriggerSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnLogTriggerError(ctx context.Context, msg *LogTriggerError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.LogTriggerError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnLogTriggerCleanUpError(ctx context.Context, msg *LogTriggerCleanUpError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.LogTriggerCleanUpError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnTriggerEventDroppedError(ctx context.Context, msg *LogTriggerEventDroppedError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.LogTriggerEventDroppedError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

// -- FilterLogs --

func (m *Metrics) OnFilterLogsSuccess(ctx context.Context, msg *FilterLogsSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.FilterLogsSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnFilterLogsError(ctx context.Context, msg *FilterLogsError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.FilterLogsError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

// -- BalanceAt --

func (m *Metrics) OnBalanceAtSuccess(ctx context.Context, msg *BalanceAtSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.BalanceAtSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnBalanceAtError(ctx context.Context, msg *BalanceAtError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.BalanceAtError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

// -- EstimateGas --

func (m *Metrics) OnEstimateGasSuccess(ctx context.Context, msg *EstimateGasSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.EstimateGasSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnEstimateGasError(ctx context.Context, msg *EstimateGasError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.EstimateGasError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

// -- GetTransactionByHash --

func (m *Metrics) OnGetTransactionByHashSuccess(ctx context.Context, msg *GetTransactionByHashSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.GetTxByHashSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnGetTransactionByHashError(ctx context.Context, msg *GetTransactionByHashError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.GetTxByHashError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

// -- GetTransactionReceipt --

func (m *Metrics) OnGetTransactionReceiptSuccess(ctx context.Context, msg *GetTransactionReceiptSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.GetReceiptSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnGetTransactionReceiptError(ctx context.Context, msg *GetTransactionReceiptError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.GetReceiptError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

// -- HeaderByNumber --

func (m *Metrics) OnHeaderByNumberSuccess(ctx context.Context, msg *HeaderByNumberSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.HeaderByNumberSuccess.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

func (m *Metrics) OnHeaderByNumberError(ctx context.Context, msg *HeaderByNumberError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.HeaderByNumberError.basic.RecordEmit(ctx, start, emit, msg.Attributes()...)
	return nil
}

// Attributes methods attach metric labels for each message type

func (r *CallContractSuccess) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.Int64("block_number", r.Req.GetBlockNumber()),
		attribute.String("contract_address", r.Req.GetContractAddress()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *CallContractError) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.Int64("block_number", r.Req.GetBlockNumber()),
		attribute.String("contract_address", r.Req.GetContractAddress()),
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *WriteReportSuccess) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("receiver", getReceiver(r.Req.GetReceiver())),
	}, r.ExecutionContext.Attributes()...)
}

func (r *WriteReportError) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("receiver", getReceiver(r.Req.GetReceiver())),
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *LogTriggerSuccess) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("trigger_id", r.GetTriggerID()),
		attribute.Int64("log_count", int64(r.GetLogCount())),
	}, r.ExecutionContext.Attributes()...)
}

func (r *LogTriggerError) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("trigger_id", r.GetTriggerID()),
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *LogTriggerCleanUpError) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *LogTriggerEventDroppedError) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("trigger_id", r.GetTriggerID()),
		attribute.String("tx_hash", r.GetTxHash()),
		attribute.String("block_hash", r.GetBlockHash()),
		attribute.Int64("log_index", r.GetLogIndex()),
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *FilterLogsSuccess) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.Int64("from_block", r.Req.GetFromBlock()),
		attribute.Int64("to_block", r.Req.GetToBlock()),
		attribute.Int64("log_count", int64(r.GetLogCount())),
	}, r.ExecutionContext.Attributes()...)
}

func (r *FilterLogsError) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.Int64("from_block", r.Req.GetFromBlock()),
		attribute.Int64("to_block", r.Req.GetToBlock()),
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *BalanceAtSuccess) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("account", r.Req.GetAccount()),
		attribute.Int64("block_number", r.Req.GetBlockNumber()),
		attribute.String("balance", r.GetBalance()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *BalanceAtError) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("account", r.Req.GetAccount()),
		attribute.Int64("block_number", r.Req.GetBlockNumber()),
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *EstimateGasSuccess) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("from", r.Req.GetFrom()),
		attribute.String("to", r.Req.GetTo()),
		attribute.Int64("gas", r.GetGas()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *EstimateGasError) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("from", r.Req.GetFrom()),
		attribute.String("to", r.Req.GetTo()),
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *GetTransactionByHashSuccess) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("hash", r.Req.GetHash()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *GetTransactionByHashError) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("hash", r.Req.GetHash()),
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *GetTransactionReceiptSuccess) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("hash", r.Req.GetHash()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *GetTransactionReceiptError) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("hash", r.Req.GetHash()),
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *HeaderByNumberSuccess) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.Int64("block_number", r.Req.GetBlockNumber()),
	}, r.ExecutionContext.Attributes()...)
}

func (r *HeaderByNumberError) Attributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.Int64("block_number", r.Req.GetBlockNumber()),
	}, r.ExecutionContext.Attributes()...)
}

func getReceiver(receiver []byte) string {
	if receiver != nil {
		return common.Bytes2Hex(receiver)
	}
	return "nil receiver"
}
