package common

import (
	"errors"
	"strings"

	"github.com/google/uuid"

	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

// GetRequestID generates a unique request ID by combining the method name,
// additional parts and a UUID. The additional parts can be identifiers like workflowID,
// workflowExecutionID, etc
func GetRequestID(methodName string, parts ...string) string {
	id := append([]string{methodName}, parts...)
	id = append(id, uuid.New().String())
	return strings.Join(id, "/")
}

// UserErrorCode returns the appropriate error code for a user-facing HTTP action error.
func UserErrorCode(err error) caperrors.ErrorCode {
	var limitErr limits.LimitError
	if errors.As(err, &limitErr) {
		return caperrors.LimitExceeded
	}
	return caperrors.InvalidArgument
}
