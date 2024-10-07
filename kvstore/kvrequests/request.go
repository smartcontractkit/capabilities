package kvrequests

import (
	"bytes"
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
}

func (r *Request) ID() RequestID {
	return RequestID(fmt.Sprintf("%s_%s_%s", requestKindToString[r.Type], r.ReferenceID, r.WorkflowExecutionID))
}

func (r Request) String() string {
	return fmt.Sprintf("Request{ID: \"%s\", pairs: %s}", r.ID(), r.KVPairs)
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
