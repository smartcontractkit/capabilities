package oracle

import (
	"maps"
	"slices"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
)

type Outcome struct {
	// This is the local (in-memory) namespaced key-value store
	Namespaces          map[string]kvrequests.KVPairs
	NamespaceReferences map[string][]string
	CompletedRequests   []kvrequests.Request
}

func NewOutcome() Outcome {
	return Outcome{
		NamespaceReferences: make(map[string][]string),
		Namespaces:          make(map[string]kvrequests.KVPairs),
		CompletedRequests:   make([]kvrequests.Request, 0),
	}
}

func (o *Outcome) AddNamespaceReferences(namespace string, workflowID string) {
	if o.NamespaceReferences[namespace] == nil {
		o.NamespaceReferences[namespace] = make([]string, 0)
	}
	if o.Namespaces[namespace] == nil {
		o.Namespaces[namespace] = make(kvrequests.KVPairs)
	}

	// Check if the reference is already assigned
	if slices.Contains(o.NamespaceReferences[namespace], workflowID) {
		return
	}

	o.NamespaceReferences[namespace] = append(o.NamespaceReferences[namespace], workflowID)
}

func (o *Outcome) RemoveNamespaceReference(namespace string, workflowID string) {
	if o.NamespaceReferences[namespace] == nil {
		return
	}

	for i, reference := range o.NamespaceReferences[namespace] {
		if reference == workflowID {
			o.NamespaceReferences[namespace] = append(o.NamespaceReferences[namespace][:i], o.NamespaceReferences[namespace][i+1:]...)
			break
		}
	}

	// If there are no more references to the namespace, delete the namespace
	if len(o.NamespaceReferences[namespace]) == 0 {
		delete(o.Namespaces, namespace)
	}
}

func (o *Outcome) Write(namespace string, kvPairs kvrequests.KVPairs) {
	if o.Namespaces[namespace] == nil {
		o.Namespaces[namespace] = make(kvrequests.KVPairs)
	}

	maps.Copy(o.Namespaces[namespace], kvPairs)
}

func (o *Outcome) Read(namespace string, kvPairs kvrequests.KVPairs) kvrequests.KVPairs {
	keysWithValues := make(kvrequests.KVPairs)
	for key := range kvPairs {
		val, ok := o.Namespaces[namespace][key]
		if !ok {
			keysWithValues[key] = make([]byte, 0)
		} else {
			keysWithValues[key] = val
		}
	}
	return keysWithValues
}
