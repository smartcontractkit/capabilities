package actions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/monitoring"
)

const (
	failedToRetrieveTxHashErrorMsg = "failed to retrieve tx hash for report"
)

type TxHashRetrievalResult string

const (
	TxHashRetrievalResultFound             TxHashRetrievalResult = "Found"
	TxHashRetrievalResultNotFound          TxHashRetrievalResult = "NotFound"
	TxHashRetrievalResultFetchError        TxHashRetrievalResult = "FetchError"
	TxHashRetrievalResultUnexpectedSuccess TxHashRetrievalResult = "UnexpectedSuccess"
)

type TxHashLookupType string

const (
	TxHashLookupTypeSuccessful TxHashLookupType = "SuccessfulTransmission"
	TxHashLookupTypeFailed     TxHashLookupType = "FailedTransmission"
)

var ErrUnexpectedSuccessfulTransmission = errors.New("unexpected successful transmission")

type TxHashRetriever struct {
	forwarderClient   CREForwarderClient
	transmissionID    TransmissionID
	lggr              logger.SugaredLogger
	beholderProcessor beholder.ProtoProcessor
	messageBuilder    *monitoring.MessageBuilder
	telemetryContext  monitoring.TelemetryContext
}

type TxHashRetrieverOption func(*TxHashRetriever)

func WithTxHashRetrieverMonitoring(
	beholderProcessor beholder.ProtoProcessor,
	messageBuilder *monitoring.MessageBuilder,
	telemetryContext monitoring.TelemetryContext,
) TxHashRetrieverOption {
	return func(r *TxHashRetriever) {
		r.beholderProcessor = beholderProcessor
		r.messageBuilder = messageBuilder
		r.telemetryContext = telemetryContext
	}
}

func NewTxHashRetriever(
	forwarderClient CREForwarderClient,
	lggr logger.SugaredLogger,
	transmissionID TransmissionID,
	opts ...TxHashRetrieverOption,
) TxHashRetriever {
	r := TxHashRetriever{
		forwarderClient: forwarderClient,
		transmissionID:  transmissionID,
		lggr:            lggr,
	}
	for _, opt := range opts {
		opt(&r)
	}
	return r
}

type eventDetails struct {
	txHash    string
	ledger    uint32
	isSuccess bool
}

func (d eventDetails) String() string {
	resultStr := "success"
	if !d.isSuccess {
		resultStr = "failed"
	}
	return fmt.Sprintf("hash=%s ledger=%d result=%s", d.txHash, d.ledger, resultStr)
}

type eventDetailsList []eventDetails

func (l eventDetailsList) String() string {
	if len(l) == 0 {
		return "[]"
	}
	parts := make([]string, len(l))
	for i, d := range l {
		parts[i] = d.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func (r *TxHashRetriever) GetSuccessfulTransmissionHash(ctx context.Context) (string, error) {
	details, phaseStart, err := r.fetchAndParseEvents(ctx, TxHashLookupTypeSuccessful)
	if err != nil {
		return "", err
	}
	for _, d := range details {
		if d.isSuccess {
			if r.monitoringEnabled() {
				monitoring.EmitInitiated(ctx, r.lggr, r.beholderProcessor, r.messageBuilder.BuildWriteReportTxHashRetrievalPhase(
					r.telemetryContext,
					string(TxHashRetrievalResultFound),
					int64(math.Max(float64(time.Since(phaseStart).Milliseconds()), 0)),
					d.txHash,
					string(TxHashLookupTypeSuccessful),
				))
			}
			return d.txHash, nil
		}
	}
	r.lggr.Errorw("No successful transmission found", "txCount", len(details), "transactions", details.String())
	if r.monitoringEnabled() {
		monitoring.EmitInitiated(ctx, r.lggr, r.beholderProcessor, r.messageBuilder.BuildWriteReportTxHashRetrievalPhase(
			r.telemetryContext,
			string(TxHashRetrievalResultNotFound),
			int64(math.Max(float64(time.Since(phaseStart).Milliseconds()), 0)),
			"",
			string(TxHashLookupTypeSuccessful),
		))
	}
	return "", fmt.Errorf("no successful transmission found. Found %d transactions (all failed): %s",
		len(details), details)
}

func (r *TxHashRetriever) GetFailedTransmissionHash(ctx context.Context) (string, error) {
	hash, _, err := r.GetFailedTransmissionHashWithCount(ctx)
	return hash, err
}

func (r *TxHashRetriever) GetFailedTransmissionHashWithCount(ctx context.Context) (string, int, error) {
	details, phaseStart, err := r.fetchAndParseEvents(ctx, TxHashLookupTypeFailed)
	if err != nil {
		return "", 0, err
	}
	for _, d := range details {
		if d.isSuccess {
			if r.monitoringEnabled() {
				monitoring.EmitInitiated(ctx, r.lggr, r.beholderProcessor, r.messageBuilder.BuildWriteReportTxHashRetrievalPhase(
					r.telemetryContext,
					string(TxHashRetrievalResultUnexpectedSuccess),
					int64(math.Max(float64(time.Since(phaseStart).Milliseconds()), 0)),
					d.txHash,
					string(TxHashLookupTypeFailed),
				))
			}
			return "", len(details), fmt.Errorf("%w, successful tx hash: %s",
				ErrUnexpectedSuccessfulTransmission, d.txHash)
		}
	}
	if len(details) == 0 {
		if r.monitoringEnabled() {
			monitoring.EmitInitiated(ctx, r.lggr, r.beholderProcessor, r.messageBuilder.BuildWriteReportTxHashRetrievalPhase(
				r.telemetryContext,
				string(TxHashRetrievalResultNotFound),
				int64(math.Max(float64(time.Since(phaseStart).Milliseconds()), 0)),
				"",
				string(TxHashLookupTypeFailed),
			))
		}
		return "", 0, fmt.Errorf("no failed transmission found")
	}

	earliestIdx := 0
	for i, d := range details {
		if d.ledger < details[earliestIdx].ledger {
			earliestIdx = i
		}
	}

	selectedHash := details[earliestIdx].txHash
	r.lggr.Debugw("Returning earliest failed transmission",
		append([]any{
			"txCount", len(details),
			"selectedTxHash", selectedHash,
		}, r.transmissionID.LogAttrs()...)...,
	)

	if r.monitoringEnabled() {
		monitoring.EmitInitiated(ctx, r.lggr, r.beholderProcessor, r.messageBuilder.BuildWriteReportTxHashRetrievalPhase(
			r.telemetryContext,
			string(TxHashRetrievalResultFound),
			int64(math.Max(float64(time.Since(phaseStart).Milliseconds()), 0)),
			selectedHash,
			string(TxHashLookupTypeFailed),
		))
	}
	return selectedHash, len(details), nil
}

func (r *TxHashRetriever) fetchAndParseEvents(ctx context.Context, lookupType TxHashLookupType) (eventDetailsList, time.Time, error) {
	phaseStart := time.Now()
	events, err := capcommon.WithPollingRetry(ctx, r.lggr, func(ctx context.Context) ([]ReportProcessedEvent, error) {
		events, fetchErr := r.forwarderClient.GetReportProcessedEvents(ctx, r.transmissionID)
		if fetchErr != nil {
			return nil, fetchErr
		}
		if len(events) == 0 {
			return nil, errors.New("no matching events found yet, retrying")
		}
		return events, nil
	})
	if err != nil {
		if r.monitoringEnabled() {
			monitoring.EmitInitiated(ctx, r.lggr, r.beholderProcessor, r.messageBuilder.BuildWriteReportTxHashRetrievalPhase(
				r.telemetryContext,
				string(TxHashRetrievalResultFetchError),
				int64(math.Max(float64(time.Since(phaseStart).Milliseconds()), 0)),
				"",
				string(lookupType),
			))
		}
		return nil, phaseStart, fmt.Errorf("%s: %w", failedToRetrieveTxHashErrorMsg, err)
	}

	return buildEventDetails(events), phaseStart, nil
}

func (r *TxHashRetriever) monitoringEnabled() bool {
	return r.beholderProcessor != nil && r.messageBuilder != nil
}

func buildEventDetails(events []ReportProcessedEvent) eventDetailsList {
	details := make(eventDetailsList, len(events))
	for i, e := range events {
		details[i] = eventDetails{
			txHash:    e.TxHash,
			ledger:    e.Ledger,
			isSuccess: e.Success,
		}
	}
	return details
}
