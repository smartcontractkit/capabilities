package kvrequests

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequest(t *testing.T) {
	tests := []struct {
		name           string
		request        Request
		expectedID     RequestID
		expectedString string
	}{
		{
			name: "WriteRequest",
			request: Request{
				Type:                RequestTypeWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
				Status:              RequestStatusPending,
			},
			expectedID:     "write_ref123_workflow456",
			expectedString: `{ID: "write_ref123_workflow456", status: "pending", pairs: {"baz": "bad", "foo": "bar"}}`,
		},
		{
			name: "ReadRequest",
			request: Request{
				Type:                RequestTypeRead,
				ReferenceID:         "ref789",
				WorkflowExecutionID: "workflow012",
				KVPairs:             map[string][]byte{"key2": []byte("value2")},
				Status:              RequestStatusCompleted,
			},
			expectedID:     "read_ref789_workflow012",
			expectedString: `{ID: "read_ref789_workflow012", status: "completed", pairs: {"key2": "value2"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedID, tt.request.ID())
			assert.Equal(t, tt.expectedString, tt.request.String())
		})
	}
}

func TestRequestEqual(t *testing.T) {
	tests := []struct {
		name     string
		request1 Request
		request2 Request
		expected bool
	}{
		{
			name: "EqualRequests",
			request1: Request{
				Type:                RequestTypeWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			request2: Request{
				Type:                RequestTypeWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: true,
		},
		{
			name: "DifferentTypes",
			request1: Request{
				Type:                RequestTypeWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			request2: Request{
				Type:                RequestTypeRead,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: false,
		},
		{
			name: "DifferentReferenceID",
			request1: Request{
				Type:                RequestTypeWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			request2: Request{
				Type:                RequestTypeWrite,
				ReferenceID:         "ref124",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: false,
		},
		{
			name: "DifferentWorkflowExecutionID",
			request1: Request{
				Type:                RequestTypeWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			request2: Request{
				Type:                RequestTypeWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow457",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: false,
		},
		{
			name: "DifferentKVPairs",
			request1: Request{
				Type:                RequestTypeWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			request2: Request{
				Type:                RequestTypeWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("good")},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.request1.Equal(tt.request2))
		})
	}
}
