package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
)

const (
	failedToRetrieveTxHashErrorMsg = "failed to retrieve tx hash for report"
)

var ErrUnexpectedSuccessfulTransmission = errors.New("unexpected successful transmission")

type TxHashRetriever struct {
	forwarderClient CREForwarderClient
	transmissionID  TransmissionID
	lggr            logger.SugaredLogger
}

func NewTxHashRetriever(
	forwarderClient CREForwarderClient,
	lggr logger.SugaredLogger,
	transmissionID TransmissionID,
) TxHashRetriever {
	return TxHashRetriever{
		forwarderClient: forwarderClient,
		transmissionID:  transmissionID,
		lggr:            lggr,
	}
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
	details, err := r.fetchAndParseEvents(ctx)
	if err != nil {
		return "", err
	}
	for _, d := range details {
		if d.isSuccess {
			return d.txHash, nil
		}
	}
	r.lggr.Errorw("No successful transmission found", "txCount", len(details), "transactions", details.String())
	return "", fmt.Errorf("no successful transmission found. Found %d transactions (all failed): %s",
		len(details), details)
}

func (r *TxHashRetriever) GetFailedTransmissionHash(ctx context.Context) (string, error) {
	hash, _, err := r.GetFailedTransmissionHashWithCount(ctx)
	return hash, err
}

func (r *TxHashRetriever) GetFailedTransmissionHashWithCount(ctx context.Context) (string, int, error) {
	details, err := r.fetchAndParseEvents(ctx)
	if err != nil {
		return "", 0, err
	}
	for _, d := range details {
		if d.isSuccess {
			return "", len(details), fmt.Errorf("%w, successful tx hash: %s",
				ErrUnexpectedSuccessfulTransmission, d.txHash)
		}
	}
	if len(details) == 0 {
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

	return selectedHash, len(details), nil
}

func (r *TxHashRetriever) fetchAndParseEvents(ctx context.Context) (eventDetailsList, error) {
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
		return nil, fmt.Errorf("%s: %w", failedToRetrieveTxHashErrorMsg, err)
	}

	return buildEventDetails(events), nil
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
