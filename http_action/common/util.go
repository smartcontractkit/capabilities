package common

import (
	"strings"

	"github.com/google/uuid"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

func GetRequestID(metadata capabilities.RequestMetadata) string {
	id := []string{
		metadata.WorkflowID,
		metadata.WorkflowExecutionID,
		uuid.New().String(),
	}
	return strings.Join(id, "/")
}
