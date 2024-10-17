package kvrequests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

type RequestType int

const (
	RequestTypeUnspecified RequestType = iota
	RequestTypeWrite
	RequestTypeRead
	RequestTypeAddNamespaceReference
	RequestTypeRemoveNamespaceReference
)

func (r RequestType) String() string {
	switch r {
	case RequestTypeUnspecified:
		return "unspecified"
	case RequestTypeWrite:
		return "write"
	case RequestTypeRead:
		return "read"
	case RequestTypeAddNamespaceReference:
		return "add_namespace_user"
	case RequestTypeRemoveNamespaceReference:
		return "remove_namespace_user"
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
	KVPairs   KVPairs
	Namespace string
	Reference string
	Status    RequestStatus
	Type      RequestType
}

type RequestParams struct {
	KVPairs   KVPairs
	Namespace string
	// Reference is a unique identifier for the request within the namespace
	// For RequestTypeWrite and RequestTypeRead, it should be workflow execution ID + reference ID
	// For RequestTypeAddNamespaceReference and RequestTypeRemoveNamespaceReference, it is a workflow ID
	Reference string
	Type      RequestType
}

func NewRequest(params RequestParams) (*Request, error) {
	if params.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	if params.Type == RequestTypeUnspecified {
		return nil, fmt.Errorf("request type is required")
	}
	if params.Reference == "" {
		return nil, fmt.Errorf("reference is required")
	}
	if params.Type == RequestTypeWrite || params.Type == RequestTypeRead {
		if len(params.KVPairs) == 0 {
			return nil, fmt.Errorf("key-value pairs are required for read and write requests")
		}
	}

	return &Request{
		KVPairs:   params.KVPairs,
		Namespace: params.Namespace,
		Reference: params.Reference,
		Status:    RequestStatusPending,
		Type:      params.Type,
	}, nil
}

func (r *Request) ID() RequestID {
	return RequestID(fmt.Sprintf("%s_%s_%s", r.Type, r.Namespace, r.Reference))
}

func (r *Request) Marshal() ([]byte, error) {
	return json.Marshal(r)
}

func (r Request) String() string {
	return fmt.Sprintf("{ID: \"%s\", status: \"%s\", pairs: %s}", r.ID(), r.Status, r.KVPairs)
}

func (r Request) Equal(other Request) bool {
	if r.ID() != other.ID() {
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
