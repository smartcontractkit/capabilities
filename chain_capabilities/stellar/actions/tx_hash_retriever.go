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
	reportProcessedTopicPrefix      = "forwarder_ReportProcessed"
	defaultForwarderLookbackLedgers = int64(100)
	failedToRetrieveTxHashErrorMsg  = "failed to retrieve tx hash for report"

	txHashLookupTypeSuccessful = "SuccessfulTransmission"
	txHashLookupTypeFailed     = "FailedTransmission"
	txHashRetrievalPhase       = "EventPoll"
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
	details, err := r.fetchAndParseEvents(ctx, txHashLookupTypeSuccessful)
	if err != nil {
		return "", err
	}
	for _, d := range details {
		if d.isSuccess {
			return d.txHash, nil
		}
	}
	r.lggr.Errorw("No successful transmission found", "txCount", len(details), "transactions", details.String())
	r.emitTxHashRetrievalPhase(ctx, txHashLookupTypeSuccessful, "NotFound", time.Now(), "")
	return "", fmt.Errorf("no successful transmission found. Found %d transactions (all failed): %s",
		len(details), details)
}

func (r *TxHashRetriever) GetFailedTransmissionHash(ctx context.Context) (string, error) {
	hash, _, err := r.GetFailedTransmissionHashWithCount(ctx)
	return hash, err
}

func (r *TxHashRetriever) GetFailedTransmissionHashWithCount(ctx context.Context) (string, int, error) {
	details, err := r.fetchAndParseEvents(ctx, txHashLookupTypeFailed)
	if err != nil {
		return "", 0, err
	}
	for _, d := range details {
		if d.isSuccess {
			r.emitTxHashRetrievalPhase(ctx, txHashLookupTypeFailed, "UnexpectedSuccess", time.Now(), d.txHash)
			return "", len(details), fmt.Errorf("%w, successful tx hash: %s",
				ErrUnexpectedSuccessfulTransmission, d.txHash)
		}
	}
	if len(details) == 0 {
		r.emitTxHashRetrievalPhase(ctx, txHashLookupTypeFailed, "NotFound", time.Now(), "")
		return "", 0, fmt.Errorf("no failed transmission found")
	}

	earliestIdx := 0
	for i, d := range details {
		if d.ledger < details[earliestIdx].ledger {
			earliestIdx = i
		}
	}

	r.lggr.Debugw("Returning earliest failed transmission",
		append([]any{
			"txCount", len(details),
			"selectedTxHash", details[earliestIdx].txHash,
		}, r.transmissionID.LogAttrs()...)...,
	)

	return details[earliestIdx].txHash, len(details), nil
}

func (r *TxHashRetriever) fetchAndParseEvents(ctx context.Context, lookupType string) (eventDetailsList, error) {
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
		r.emitTxHashRetrievalPhase(ctx, lookupType, "FetchError", phaseStart, "")
		return nil, fmt.Errorf("%s: %w", failedToRetrieveTxHashErrorMsg, err)
	}

	details := buildEventDetails(events)
	selectedHash := ""
	if len(details) > 0 {
		selectedHash = details[0].txHash
	}
	r.emitTxHashRetrievalPhase(ctx, lookupType, "Found", phaseStart, selectedHash)
	return details, nil
}

func (r *TxHashRetriever) emitTxHashRetrievalPhase(ctx context.Context, lookupType, result string, phaseStart time.Time, txHash string) {
	if r.beholderProcessor == nil || r.messageBuilder == nil {
		return
	}
	monitoring.EmitInitiated(ctx, r.lggr, r.beholderProcessor, r.messageBuilder.BuildWriteReportTxHashRetrievalPhase(
		r.telemetryContext,
		txHashRetrievalPhase,
		result,
		int64(math.Max(float64(time.Since(phaseStart).Milliseconds()), 0)),
		txHash,
		lookupType,
	))
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
