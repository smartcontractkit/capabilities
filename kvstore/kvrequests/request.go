package kvrequests

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type RequestType int

const (
	RequestKindWrite RequestType = iota
	RequestKindRead
)

var requestKindToString = map[RequestType]string{
	RequestKindWrite: "write",
	RequestKindRead:  "read",
}

type RequestStatus int

const (
	RequestStatusUnspecified RequestStatus = iota
	RequestStatusPending
	RequestStatusCompleted
)

func (r RequestStatus) String() string {
	switch r {
	case RequestStatusPending:
		return "pending"
	case RequestStatusCompleted:
		return "completed"
	case RequestStatusUnspecified:
		return "unspecified"
	default:
		return "unspecified"
	}
}

type KVPairs map[string][]byte

func (k KVPairs) String() string {
	s := "{"

	for key, value := range k {
		s += fmt.Sprintf("\"%s\": \"%s\", ", key, string(value))
	}

	if len(k) > 0 {
		s = s[:len(s)-2] // Remove the last ", "
	}

	s += "} "

	return s
}

type RequestID string

type Request struct {
	Type                RequestType
	ReferenceID         string
	WorkflowExecutionID string
	KVPairs             KVPairs
	Status              RequestStatus
}

type RequestParams struct {
	Type                RequestType
	ReferenceID         string
	WorkflowExecutionID string
	KVPairs             KVPairs
}

func NewRequest(params RequestParams) *Request {
	return &Request{
		Type:                params.Type,
		ReferenceID:         params.ReferenceID,
		WorkflowExecutionID: params.WorkflowExecutionID,
		KVPairs:             params.KVPairs,
		Status:              RequestStatusPending,
	}
}

func (r *Request) ID() RequestID {
	return RequestID(fmt.Sprintf("%s_%s_%s", requestKindToString[r.Type], r.ReferenceID, r.WorkflowExecutionID))
}

func (r *Request) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

func (r Request) String() string {
	return fmt.Sprintf("{ID: \"%s\", status: \"%s\", pairs: %s}", r.ID(), r.Status, r.KVPairs)
}

func (r Request) Equal(other Request) bool {
	if r.Type != other.Type {
		return false
	}

	if r.ReferenceID != other.ReferenceID {
		return false
	}

	if r.WorkflowExecutionID != other.WorkflowExecutionID {
		return false
	}

	if !deepEqualMaps(r.KVPairs, other.KVPairs) {
		return false
	}

	return true
}

func deepEqualMaps(map1, map2 map[string][]byte) bool {
	if len(map1) != len(map2) {
		return false
	}

	for key, value1 := range map1 {
		value2, exists := map2[key]
		if !exists {
			return false
		}

		if !bytes.Equal(value1, value2) {
			return false
		}
	}

	return true
}
