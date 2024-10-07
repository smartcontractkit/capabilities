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
				Type:                RequestKindWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expectedID:     "write_ref123_workflow456",
			expectedString: `Request{ID: "write_ref123_workflow456", pairs: {"foo": "bar", "baz": "bad"} }`,
		},
		{
			name: "ReadRequest",
			request: Request{
				Type:                RequestKindRead,
				ReferenceID:         "ref789",
				WorkflowExecutionID: "workflow012",
				KVPairs:             map[string][]byte{"key2": []byte("value2")},
			},
			expectedID:     "read_ref789_workflow012",
			expectedString: `Request{ID: "read_ref789_workflow012", pairs: {"key2": "value2"} }`,
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
				Type:                RequestKindWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			request2: Request{
				Type:                RequestKindWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: true,
		},
		{
			name: "DifferentTypes",
			request1: Request{
				Type:                RequestKindWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			request2: Request{
				Type:                RequestKindRead,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: false,
		},
		{
			name: "DifferentReferenceID",
			request1: Request{
				Type:                RequestKindWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			request2: Request{
				Type:                RequestKindWrite,
				ReferenceID:         "ref124",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: false,
		},
		{
			name: "DifferentWorkflowExecutionID",
			request1: Request{
				Type:                RequestKindWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			request2: Request{
				Type:                RequestKindWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow457",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: false,
		},
		{
			name: "DifferentKVPairs",
			request1: Request{
				Type:                RequestKindWrite,
				ReferenceID:         "ref123",
				WorkflowExecutionID: "workflow456",
				KVPairs:             map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			request2: Request{
				Type:                RequestKindWrite,
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
