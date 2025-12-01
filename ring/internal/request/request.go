package request

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Request represents a routing request
type Request struct {
	RequestID  string
	Payload    []byte
	ReceivedAt time.Time
	ExpiresAt  time.Time
}

// ID returns the request ID (required by StoredRequest interface)
func (r *Request) ID() string {
	return r.RequestID
}

// ExpiryTime returns the expiry time (required by StoredRequest interface)
func (r *Request) ExpiryTime() time.Time {
	return r.ExpiresAt
}

// Copy creates a copy of the request (required by StoredRequest interface)
func (r *Request) Copy() *Request {
	payload := make([]byte, len(r.Payload))
	copy(payload, r.Payload)
	return &Request{
		RequestID:  r.RequestID,
		Payload:    payload,
		ReceivedAt: r.ReceivedAt,
		ExpiresAt:  r.ExpiresAt,
	}
}

// Hash returns the SHA256 hash of the request
func (r *Request) Hash() string {
	h := sha256.New()
	h.Write([]byte(r.RequestID))
	h.Write(r.Payload)
	return hex.EncodeToString(h.Sum(nil))
}

// MarshalBinary implements encoding.BinaryMarshaler
func (r *Request) MarshalBinary() ([]byte, error) {
	return json.Marshal(r)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (r *Request) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, r)
}

// String returns the hash of the request
func (r *Request) String() string {
	return r.Hash()
}
