package oracle

import "github.com/smartcontractkit/capabilities/kvstore/kvrequests"

type Outcome struct {
	// This is the local (in-memory) namespaced key-value store
	Namespaces        map[string]kvrequests.KVPairs
	NamespaceClients  map[string][]string
	CompletedRequests []kvrequests.Request
}

func NewOutcome() Outcome {
	return Outcome{
		NamespaceClients:  make(map[string][]string),
		Namespaces:        make(map[string]kvrequests.KVPairs),
		CompletedRequests: make([]kvrequests.Request, 0),
	}
}

func (o *Outcome) AddNamespaceReferences(namespace string, workflowID string) {
	if o.NamespaceClients[namespace] == nil {
		o.NamespaceClients[namespace] = make([]string, 0)
	}
	if o.Namespaces[namespace] == nil {
		o.Namespaces[namespace] = make(kvrequests.KVPairs)
	}

	// Check if the user is already in the namespace
	for _, user := range o.NamespaceClients[namespace] {
		if user == workflowID {
			return
		}
	}

	o.NamespaceClients[namespace] = append(o.NamespaceClients[namespace], workflowID)
}

func (o *Outcome) RemoveNamespaceReference(namespace string, workflowID string) {
	if o.NamespaceClients[namespace] == nil {
		return
	}

	for i, user := range o.NamespaceClients[namespace] {
		if user == workflowID {
			o.NamespaceClients[namespace] = append(o.NamespaceClients[namespace][:i], o.NamespaceClients[namespace][i+1:]...)
			break
		}
	}

	// If there are no more users in the namespace, delete the namespace
	if len(o.NamespaceClients[namespace]) == 0 {
		delete(o.Namespaces, namespace)
	}
}

func (o *Outcome) Write(namespace string, kvPairs kvrequests.KVPairs) {
	if o.Namespaces[namespace] == nil {
		o.Namespaces[namespace] = make(kvrequests.KVPairs)
	}

	for key, value := range kvPairs {
		o.Namespaces[namespace][key] = value
	}
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
