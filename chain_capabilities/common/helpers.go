package common

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jpillora/backoff"

	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/retry"
)

const UserError = "user error:"

// Ptr returns a pointer to the given value.
func Ptr[T any](v T) *T {
	return &v
}

// DecodeReportMetadata decodes OCR3 report metadata from raw bytes.
func DecodeReportMetadata(data []byte) (ocrtypes.Metadata, error) {
	metadata, _, err := ocrtypes.Decode(data)
	return metadata, err
}

// ParseTransmissionComponents extracts and validates the executionID and reportID
// common to all chain transmission ID construction.
func ParseTransmissionComponents(workflowExecutionID string, rawReport []byte) ([32]byte, [2]byte, error) {
	rawExecutionID, err := hex.DecodeString(workflowExecutionID)
	if err != nil {
		return [32]byte{}, [2]byte{}, err
	}
	if len(rawExecutionID) != 32 {
		return [32]byte{}, [2]byte{}, fmt.Errorf("workflowExecutionID must be 32 bytes, got %d", len(rawExecutionID))
	}

	reportMetadata, err := DecodeReportMetadata(rawReport)
	if err != nil {
		return [32]byte{}, [2]byte{}, fmt.Errorf("%s failed to decode report metadata: %v", UserError, err)
	}

	reportID, err := hex.DecodeString(reportMetadata.ReportID)
	if err != nil {
		return [32]byte{}, [2]byte{}, fmt.Errorf("%s failed to decode report ID: %v", UserError, err)
	}
	if len(reportID) != 2 {
		return [32]byte{}, [2]byte{}, fmt.Errorf("%s report ID is of wrong length: %d bytes, expected 2 bytes", UserError, len(reportID))
	}

	return [32]byte(rawExecutionID), [2]byte(reportID), nil
}

// GetError returns the appropriate capability error based on whether it is a user error.
func GetError(err error, isUserError bool) caperrors.Error {
	if isUserError {
		return NewUserError(err)
	}
	return caperrors.NewPublicSystemError(err, caperrors.Unknown)
}

// NewUserError wraps an error as a public user error.
func NewUserError(err error) caperrors.Error {
	return caperrors.NewPublicUserError(err, caperrors.Unknown)
}

// WithQuickRetry wraps a simple RPC read with retry logic.
// Uses shorter timeout (10s) and fast backoff - these calls should be sub-second.
func WithQuickRetry[T any](ctx context.Context, lggr logger.Logger, fn func(context.Context) (T, error)) (T, error) {
	return WithRetry(ctx, lggr, fn, 10*time.Second, 1*time.Second, 10)
}

// WithPollingRetry wraps an operation that polls for state changes.
// Uses longer timeout (60s) to accommodate slow chains.
func WithPollingRetry[T any](ctx context.Context, lggr logger.Logger, fn func(context.Context) (T, error)) (T, error) {
	return WithRetry(ctx, lggr, fn, 60*time.Second, 3*time.Second, 25)
}

// WithRetry executes fn with exponential backoff retry logic.
// Returns the original error from fn, not the retry wrapper error.
func WithRetry[T any](ctx context.Context, lggr logger.Logger, fn func(context.Context) (T, error), timeout, maxBackoff time.Duration, maxRetries uint) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	strategy := retry.Strategy[T]{
		Backoff:    &backoff.Backoff{Factor: 2, Min: 100 * time.Millisecond, Max: maxBackoff},
		MaxRetries: maxRetries,
	}
	result, err := strategy.Do(ctx, lggr, func(ctx context.Context) (T, error) {
		r, e := fn(ctx)
		if e != nil {
			lastErr = e
		}
		return r, e
	})
	if err != nil {
		if lastErr != nil {
			return result, lastErr
		}
		return result, err
	}
	return result, nil
}
