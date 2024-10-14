package kvrequests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequest(t *testing.T) {
	tests := []struct {
		name           string
		params         RequestParams
		status         RequestStatus
		expectedID     RequestID
		expectedString string
	}{
		{
			name: "WriteRequest",
			params: RequestParams{
				Namespace: "owner1",
				Type:      RequestTypeWrite,
				Reference: "ref123_workflow456",
				KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			status:         RequestStatusPending,
			expectedID:     "write_owner1_ref123_workflow456",
			expectedString: `{ID: "write_owner1_ref123_workflow456", status: "pending", pairs: {"baz": "bad", "foo": "bar"}}`,
		},
		{
			name: "ReadRequest",
			params: RequestParams{
				Namespace: "owner2",
				Type:      RequestTypeRead,
				Reference: "ref789_workflow012",
				KVPairs:   map[string][]byte{"key2": []byte("value2")},
			},
			status:         RequestStatusCompleted,
			expectedID:     "read_owner2_ref789_workflow012",
			expectedString: `{ID: "read_owner2_ref789_workflow012", status: "completed", pairs: {"key2": "value2"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := NewRequest(tt.params)
			require.NoError(t, err)
			req.Status = tt.status
			assert.Equal(t, tt.expectedID, req.ID())
			assert.Equal(t, tt.expectedString, req.String())
		})
	}
}

func TestRequestEqual(t *testing.T) {
	tests := []struct {
		name     string
		params1  RequestParams
		params2  RequestParams
		expected bool
	}{
		{
			name: "EqualRequests",
			params1: RequestParams{
				Namespace: "owner1",
				Type:      RequestTypeWrite,
				Reference: "ref123_workflow456",
				KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			params2: RequestParams{
				Namespace: "owner1",
				Type:      RequestTypeWrite,
				Reference: "ref123_workflow456",
				KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: true,
		},
		{
			name: "DifferentTypes",
			params1: RequestParams{
				Namespace: "owner1",
				Type:      RequestTypeWrite,
				Reference: "ref123_workflow456",
				KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			params2: RequestParams{
				Namespace: "owner1",
				Type:      RequestTypeRead,
				Reference: "ref123_workflow456",
				KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: false,
		},
		{
			name: "DifferentReferenceID",
			params1: RequestParams{
				Namespace: "owner1",
				Type:      RequestTypeWrite,
				Reference: "ref123_workflow456",
				KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			params2: RequestParams{
				Namespace: "owner1",
				Type:      RequestTypeWrite,
				Reference: "ref124_workflow456",
				KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: false,
		},
		{
			name: "DifferentWorkflowExecutionID",
			params1: RequestParams{
				Namespace: "owner1",
				Type:      RequestTypeWrite,
				Reference: "ref123_workflow456",
				KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			params2: RequestParams{
				Namespace: "owner1",
				Type:      RequestTypeWrite,
				Reference: "ref123_workflow457",
				KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			expected: false,
		},
		{
			name: "DifferentKVPairs",
			params1: RequestParams{
				Namespace: "owner1",
				Type:      RequestTypeWrite,
				Reference: "ref123_workflow456",
				KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("bad")},
			},
			params2: RequestParams{
				Namespace: "owner1",
				Type:      RequestTypeWrite,
				Reference: "ref123_workflow456",
				KVPairs:   map[string][]byte{"foo": []byte("bar"), "baz": []byte("good")},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req1, err := NewRequest(tt.params1)
			require.NoError(t, err)
			req2, err := NewRequest(tt.params2)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, req1.Equal(*req2))
		})
	}
}
