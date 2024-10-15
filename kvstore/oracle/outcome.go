package oracle

import "github.com/smartcontractkit/capabilities/kvstore/kvrequests"

type Outcome struct {
	// This is the local (in-memory) namespaced key-value store
	Namespaces        map[string]kvrequests.KVPairs
	CompletedRequests []kvrequests.Request
}

func NewOutcome() Outcome {
	return Outcome{
		Namespaces:        make(map[string]kvrequests.KVPairs),
		CompletedRequests: make([]kvrequests.Request, 0),
	}
}

func (o *Outcome) RemoveNamespace(namespace string) {
	delete(o.Namespaces, namespace)
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
