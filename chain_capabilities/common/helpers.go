package capcommon

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jpillora/backoff"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/retry"

	commonmon "github.com/smartcontractkit/capabilities/libs/monitoring"
)

const UserError = "user error:"

// Ptr returns a pointer to the given value.
//
//go:fix inline
func Ptr[T any](v T) *T {
	return new(v)
}

// RequestID builds a stable request identifier from workflow metadata.
func RequestID(meta capabilities.RequestMetadata) string {
	return commonmon.RequestID(meta.WorkflowExecutionID, meta.ReferenceID)
}

// DecodeReportMetadata decodes OCR3 report metadata from raw bytes.
func DecodeReportMetadata(data []byte) (ocrtypes.Metadata, error) {
	metadata, _, err := ocrtypes.Decode(data)
	return metadata, err
}

// ValidateReportMetadata decodes the metadata embedded in rawReport and verifies
// it matches the workflow identifiers in the request metadata.
func ValidateReportMetadata(requestMetadata capabilities.RequestMetadata, rawReport []byte) error {
	reportMetadata, err := DecodeReportMetadata(rawReport)
	if err != nil {
		return err
	}

	if reportMetadata.Version != 1 {
		return fmt.Errorf("unsupported report version: %d", reportMetadata.Version)
	}

	if reportMetadata.ExecutionID != requestMetadata.WorkflowExecutionID {
		return fmt.Errorf("workflowExecutionID in the report does not match WorkflowExecutionID in the request metadata. Report WorkflowExecutionID: %s, request WorkflowExecutionID: %s", reportMetadata.ExecutionID, requestMetadata.WorkflowExecutionID)
	}

	// case-insensitive verification of the owner address (so that a check-summed address matches its non-checksummed version).
	if !strings.EqualFold(reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner) {
		return fmt.Errorf("workflowOwner in the report does not match WorkflowOwner in the request metadata. Report WorkflowOwner: %s, request WorkflowOwner: %s", reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner)
	}

	// workflowNames are padded to 10 bytes (20 hex chars)
	reqName := requestMetadata.WorkflowName
	if len(reqName) < 20 {
		reqName += strings.Repeat("0", 20-len(reqName))
	}
	if reportMetadata.WorkflowName != reqName {
		return fmt.Errorf("workflowName in the report does not match WorkflowName in the request metadata. Report WorkflowName: %s, request WorkflowName: %s", reportMetadata.WorkflowName, reqName)
	}

	if reportMetadata.WorkflowID != requestMetadata.WorkflowID {
		return fmt.Errorf("workflowID in the report does not match WorkflowID in the request metadata. Report WorkflowID: %s, request WorkflowID: %s", reportMetadata.WorkflowID, requestMetadata.WorkflowID)
	}

	return nil
}

// ValidateReportMetadataWithPrefix runs ValidateReportMetadata and prefixes any
// resulting error, e.g. with UserError to mark it as user-facing.
func ValidateReportMetadataWithPrefix(prefix string, requestMetadata capabilities.RequestMetadata, rawReport []byte) error {
	if err := ValidateReportMetadata(requestMetadata, rawReport); err != nil {
		return fmt.Errorf("%s %w", prefix, err)
	}
	return nil
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
	return caperrors.NewPublicUserError(err, UserErrorCode(err))
}

// UserErrorCode returns the appropriate error code for a user-facing error.
func UserErrorCode(err error) caperrors.ErrorCode {
	var limitErr limits.LimitError
	if errors.As(err, &limitErr) {
		return caperrors.LimitExceeded
	}
	return caperrors.Unknown
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

// MaxRequestTimeout returns the maximum RequestTimeout configured across
// capabilityID's CapabilityMethodConfig entries for donID. Method configs for
// WriteReport and LogTrigger methods are excluded — their timeout semantics
// differ from regular executable methods. If the config can't be fetched or no
// eligible RemoteExecutableConfig.RequestTimeout values are found, it returns
// fallback.
func MaxRequestTimeout(ctx context.Context, registry core.CapabilitiesRegistry, capabilityID string, donID uint32, fallback time.Duration, lggr logger.Logger) time.Duration {
	if registry == nil {
		return fallback
	}

	cfg, err := WithPollingRetry(ctx, lggr, func(ctx context.Context) (capabilities.CapabilityConfiguration, error) {
		return registry.ConfigForCapability(ctx, capabilityID, donID)
	})
	if err != nil {
		lggr.Errorw("failed getting config for capability", "capabilityID", capabilityID, "error", err)
		return fallback
	}

	var maxTimeout time.Duration
	var count int
	for method, methodCfg := range cfg.CapabilityMethodConfig {
		if isNonReadMethod(method) {
			continue
		}
		if methodCfg.RemoteExecutableConfig == nil || methodCfg.RemoteExecutableConfig.RequestTimeout == 0 {
			continue
		}
		if methodCfg.RemoteExecutableConfig.RequestTimeout > maxTimeout {
			maxTimeout = methodCfg.RemoteExecutableConfig.RequestTimeout
		}
		count++
	}
	if count == 0 {
		return fallback
	}
	return maxTimeout
}

func isNonReadMethod(method string) bool {
	switch method {
	case "WriteReport", "LogTrigger":
		return true
	default:
		return false
	}
}
