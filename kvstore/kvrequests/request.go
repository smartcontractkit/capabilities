package kvrequests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

type RequestType int

const (
	RequestTypeWrite RequestType = iota
	RequestTypeRead
	RequestTypeAddNamespace
	RequestTypeRemoveNamespace
)

func (r RequestType) String() string {
	switch r {
	case RequestTypeWrite:
		return "write"
	case RequestTypeRead:
		return "read"
	case RequestTypeAddNamespace:
		return "add_namespace"
	case RequestTypeRemoveNamespace:
		return "remove_namespace"
	}
	return "unspecified"
}

type RequestStatus int

const (
	RequestStatusUnspecified RequestStatus = iota
	RequestStatusPending
	RequestStatusCompleted
)

func (r RequestStatus) String() string {
	switch r {
	case RequestStatusUnspecified:
		return "unspecified"
	case RequestStatusPending:
		return "pending"
	case RequestStatusCompleted:
		return "completed"
	}
	return "unspecified"
}

type KVPairs map[string][]byte

func (k KVPairs) String() string {
	keys := make([]string, 0, len(k))
	for key := range k {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	s := "{"
	for _, key := range keys {
		s += fmt.Sprintf("\"%s\": \"%s\", ", key, string(k[key]))
	}

	if len(k) > 0 {
		s = s[:len(s)-2] // Remove the last ", "
	}

	s += "}"

	return s
}

type RequestID string

type Request struct {
	Type      RequestType
	Reference string
	KVPairs   KVPairs
	Status    RequestStatus
}

type RequestParams struct {
	Type      RequestType
	Reference string
	KVPairs   KVPairs
}

func NewRequest(params RequestParams) *Request {
	return &Request{
		Type:      params.Type,
		Reference: params.Reference,
		KVPairs:   params.KVPairs,
		Status:    RequestStatusPending,
	}
}

func (r *Request) ID() RequestID {
	return RequestID(fmt.Sprintf("%s_%s", r.Type, r.Reference))
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

	if r.Reference != other.Reference {
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
