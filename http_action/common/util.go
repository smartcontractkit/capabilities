package common

import (
	"strings"

	"github.com/google/uuid"
)

// GetRequestID generates a unique request ID by combining the method name,
// additional parts and a UUID. The additional parts can be identifiers like workflowID,
// workflowExecutionID, etc
func GetRequestID(methodName string, parts ...string) string {
	id := append([]string{methodName}, parts...)
	id = append(id, uuid.New().String())
	return strings.Join(id, "/")
}
