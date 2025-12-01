package request

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequest_ID(t *testing.T) {
	req := &Request{
		RequestID: "test-request-123",
		Payload:   []byte("test payload"),
	}
	
	assert.Equal(t, "test-request-123", req.ID())
}

func TestRequest_ExpiryTime(t *testing.T) {
	now := time.Now()
	expiry := now.Add(5 * time.Minute)
	
	req := &Request{
		RequestID:  "test-request-123",
		Payload:    []byte("test payload"),
		ReceivedAt: now,
		ExpiresAt:  expiry,
	}
	
	assert.Equal(t, expiry, req.ExpiryTime())
}

func TestRequest_Hash(t *testing.T) {
	req := &Request{
		RequestID: "test-request-123",
		Payload:   []byte("test payload"),
	}
	
	hash := req.Hash()
	
	// Hash should not be empty
	assert.NotEmpty(t, hash)
	
	// Same request should produce same hash
	hash2 := req.Hash()
	assert.Equal(t, hash, hash2)
	
	// Different request should produce different hash
	req2 := &Request{
		RequestID: "test-request-456",
		Payload:   []byte("test payload"),
	}
	hash3 := req2.Hash()
	assert.NotEqual(t, hash, hash3)
}

func TestRequest_String(t *testing.T) {
	req := &Request{
		RequestID: "test-request-123",
		Payload:   []byte("test payload"),
	}
	
	// String should return the hash
	str := req.String()
	assert.Equal(t, req.Hash(), str)
}

func TestRequest_Copy(t *testing.T) {
	now := time.Now()
	expiry := now.Add(5 * time.Minute)
	
	req := &Request{
		RequestID:  "test-request-123",
		Payload:    []byte("test payload"),
		ReceivedAt: now,
		ExpiresAt:  expiry,
	}
	
	copy := req.Copy()
	
	// Copy should have same values
	assert.Equal(t, req.RequestID, copy.RequestID)
	assert.Equal(t, req.Payload, copy.Payload)
	assert.Equal(t, req.ReceivedAt, copy.ReceivedAt)
	assert.Equal(t, req.ExpiresAt, copy.ExpiresAt)
	
	// But should be different instances
	assert.NotSame(t, &req.Payload, &copy.Payload)
	
	// Modifying copy should not affect original
	copy.Payload[0] = 'X'
	assert.NotEqual(t, req.Payload[0], copy.Payload[0])
}

func TestRequest_MarshalBinary(t *testing.T) {
	now := time.Now()
	expiry := now.Add(5 * time.Minute)
	
	req := &Request{
		RequestID:  "test-request-123",
		Payload:    []byte("test payload"),
		ReceivedAt: now,
		ExpiresAt:  expiry,
	}
	
	data, err := req.MarshalBinary()
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestRequest_UnmarshalBinary(t *testing.T) {
	now := time.Now().Truncate(time.Second) // Truncate for JSON serialization
	expiry := now.Add(5 * time.Minute)
	
	req := &Request{
		RequestID:  "test-request-123",
		Payload:    []byte("test payload"),
		ReceivedAt: now,
		ExpiresAt:  expiry,
	}
	
	data, err := req.MarshalBinary()
	require.NoError(t, err)
	
	req2 := &Request{}
	err = req2.UnmarshalBinary(data)
	require.NoError(t, err)
	
	assert.Equal(t, req.RequestID, req2.RequestID)
	assert.Equal(t, req.Payload, req2.Payload)
	// Times may have slightly different precision after JSON marshal/unmarshal
	assert.WithinDuration(t, req.ReceivedAt, req2.ReceivedAt, time.Second)
	assert.WithinDuration(t, req.ExpiresAt, req2.ExpiresAt, time.Second)
}

