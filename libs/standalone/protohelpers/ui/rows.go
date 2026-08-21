package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// The shapes the page is sent. A row is one trigger event as every instance saw
// it, which is the table the page draws: the event down the side, the instances
// across the top.

// Node is one instance's delivery of one event.
type Node struct {
	Instance int    `json:"instance"`
	Label    string `json:"label"`
	// At is when this process saw it, not when the instance produced it: what is
	// being looked for is instances disagreeing or lagging, and the only clock all
	// of them share is this one.
	At time.Time `json:"at"`
	// PayloadID is the hash of what this instance sent, and PayloadIndex is which
	// of the row's distinct payloads that is.
	//
	// The payload itself is on the row rather than here: instances agreeing is the
	// normal case, so holding it per node would send the same JSON once per
	// instance to say they matched.
	PayloadID    string `json:"payloadId,omitempty"`
	PayloadIndex int    `json:"payloadIndex"`
	Error        string `json:"error,omitempty"`
}

// payloadSet is the distinct payloads of one comparison, with their hashes.
//
// Held once rather than per instance, because instances agreeing is the normal
// case: four instances that answered identically would otherwise send the same
// JSON four times to say they matched. Whoever is comparing them points at an
// entry instead.
//
// Used for both halves of the page - a trigger's event and a fan-out's response -
// because they are the same question asked twice: what did each instance say, and
// did they say the same thing.
type payloadSet struct {
	// PayloadIDs and Payloads are in the same order, so hash i describes payload i
	// and an index points into both.
	//
	// The order is the order they arrived in, so payload 0 is whatever came first -
	// not whichever instance is lowest-numbered, since instances answer
	// concurrently and race. What matters is that the order never changes once set,
	// so a payload does not move columns while it is being read.
	PayloadIDs []string          `json:"payloadIds"`
	Payloads   []json.RawMessage `json:"payloads"`
	// Diverged is more than one distinct payload: the instances disagree, which is
	// what these tables exist to show.
	Diverged bool `json:"diverged"`
}

// add returns where this payload sits in the set, appending it if it is one the
// set has not seen.
//
// The hash is supplied rather than taken from the JSON, because for a proto it
// cannot be taken from the JSON: protojson deliberately varies its whitespace,
// so equal messages do not have equal JSON bytes and hashing them would report
// agreement as disagreement. An event hashes the message, a fan-out response
// hashes the bytes it was given, and both arrive here already decided.
//
// Since entries are deduplicated by hash, the JSON kept is whichever arrived
// first for that hash - so the cosmetic differences never show up beside each
// other either.
func (p *payloadSet) add(id string, payload json.RawMessage) int {
	if id == "" || len(payload) == 0 {
		// A failure carries no payload, so there is nothing to point at. Pointing
		// at zero would read as agreeing with whoever sent it.
		return -1
	}

	for i, existing := range p.PayloadIDs {
		if existing == id {
			return i
		}
	}

	p.PayloadIDs = append(p.PayloadIDs, id)
	p.Payloads = append(p.Payloads, payload)
	p.Diverged = len(p.PayloadIDs) > 1
	return len(p.PayloadIDs) - 1
}

// shortHash is how a payload is identified when there is nothing better to hash
// than the bytes themselves - which is the case for a response, already rendered
// by the form generator.
//
// Long enough that two different payloads in one table colliding is not something
// to plan for, short enough to read across a row of columns.
func shortHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])[:payloadIDLength]
}

// payloadIDLength is how much of a hash is shown.
const payloadIDLength = 8

// hashAt is the hash of the payload at an index, or empty for no payload.
func (p *payloadSet) hashAt(index int) string {
	if index < 0 || index >= len(p.PayloadIDs) {
		return ""
	}
	return p.PayloadIDs[index]
}

// copy is a copy nothing else holds.
func (p payloadSet) copy() payloadSet {
	p.PayloadIDs = append([]string(nil), p.PayloadIDs...)
	p.Payloads = append([]json.RawMessage(nil), p.Payloads...)
	return p
}

// Row is one event, keyed by the ID the trigger gave it, with a column per
// instance that delivered it.
type Row struct {
	ID    string    `json:"id"`
	First time.Time `json:"first"`
	Nodes []Node    `json:"nodes"`

	payloadSet
}

// add folds one instance's delivery into the row.
//
// Called with the subscription locked.
func (r *Row) add(node Node, payload json.RawMessage) {
	node.PayloadIndex = r.payloadSet.add(node.PayloadID, payload)

	for i, existing := range r.Nodes {
		if existing.Instance == node.Instance {
			// A redelivery of the same event: the latest is what is shown, since a
			// row is what each instance has said about this event rather than a log
			// of how many times it said it.
			r.Nodes[i] = node
			return
		}
	}

	r.Nodes = append(r.Nodes, node)
	sort.Slice(r.Nodes, func(i, j int) bool { return r.Nodes[i].Instance < r.Nodes[j].Instance })
}

// clone is a copy nothing else holds, for handing to a reader while deliveries
// continue.
func (r *Row) clone() Row {
	copied := *r
	copied.Nodes = append([]Node(nil), r.Nodes...)
	copied.payloadSet = r.payloadSet.copy()
	return copied
}

// Attached is one instance registered under a subscription.
type Attached struct {
	Instance int    `json:"instance"`
	Label    string `json:"label"`
}

// Status is a subscription as the sidebar shows it.
type Status struct {
	TriggerID    string     `json:"triggerId"`
	CapabilityID string     `json:"capabilityId"`
	Service      string     `json:"service"`
	Method       string     `json:"method"`
	Instances    []Attached `json:"instances"`
	Events       int        `json:"events"`
	// Readers is how many streams are watching. Zero with InGrace set is a
	// subscription counting down to being unregistered.
	Readers int  `json:"readers"`
	InGrace bool `json:"inGrace"`
	Closed  bool `json:"closed"`

	Created time.Time `json:"created"`
	// Rows is the table, sent on the snapshot a reader opens with rather than on
	// every update.
	Rows []Row `json:"rows,omitempty"`
}

// Message types, as the page switches on them.
const (
	// MessageSnapshot is the whole subscription, sent when a reader attaches. A
	// reader that reconnects is caught up by it rather than replaying.
	MessageSnapshot = "snapshot"
	// MessageRow is one event's row, whole. Sent whole rather than as the one
	// instance's delta, so a reader that missed something is corrected by the next
	// update instead of staying wrong.
	MessageRow = "row"
	// MessageAttached is the instance list changing: another instance joined, or
	// one was detached.
	MessageAttached = "attached"
	// MessageClosed is the subscription being unregistered. The table stays on
	// screen; nothing more will arrive in it.
	MessageClosed = "closed"
)

// Message is what a reader is sent.
type Message struct {
	Type      string  `json:"type"`
	TriggerID string  `json:"triggerId"`
	Row       *Row    `json:"row,omitempty"`
	Status    *Status `json:"status,omitempty"`
}
