// Package service is the gateway: a customer's trigger request goes through it,
// and a workflow's outbound HTTP request goes out through it.
//
// The same two jobs the gateway does today, in a binary of its own rather than
// inside a node. What a customer sends and receives is unchanged, because the
// customer is not part of this migration.
package service

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	gateway "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

// metadata is which workflows exist and who may ask for them.
//
// A node tells it, and one node is not enough. A workflow's authorised keys
// decide who may run it, so taking one node's word would let a single
// compromised node hand out permission to run anything. What counts is agreement:
// a fact is true when F+1 nodes of the DON report it identically, which is the
// same threshold the gateway applies today and the same one a DON's own consensus
// uses.
type metadata struct {
	lggr logger.Logger

	// agreement is how many nodes must report the same thing, F+1.
	agreement int

	// stale is how long a node's report is counted for. A node that stopped
	// reporting stops voting, so a workflow that was deleted stops being triggerable
	// rather than lingering on the last thing anyone said about it.
	stale time.Duration

	mu sync.RWMutex

	// reports is workflowID -> node -> what that node last said, and when.
	reports map[string]map[string]report

	agreed map[string]gateway.WorkflowMetadata

	// The selector a customer names a workflow by, to the ID the DON knows it as.
	byReference map[reference]string
}

type report struct {
	metadata gateway.WorkflowMetadata
	digest   string
	at       time.Time
}

type reference struct {
	owner string
	name  string
	tag   string
}

func newMetadata(lggr logger.Logger, agreement int, stale time.Duration) *metadata {
	if agreement < 1 {
		agreement = 1
	}
	return &metadata{
		lggr:        lggr,
		agreement:   agreement,
		stale:       stale,
		reports:     map[string]map[string]report{},
		agreed:      map[string]gateway.WorkflowMetadata{},
		byReference: map[reference]string{},
	}
}

// Record merges rather than replaces: a node pushes one workflow when it
// registers it and answers a pull in batches, so a message is part of the picture
// rather than the whole of it. Forgetting is left to staleness - a workflow the
// node stopped running is one it stops reporting, and its report ages out.
//
// The node is the authenticated one: the transport established that, so nothing
// here trusts what the message claims about its sender.
func (m *metadata) Record(node string, reported []gateway.WorkflowMetadata) error {
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, workflow := range reported {
		digest, err := workflow.Digest()
		if err != nil {
			return fmt.Errorf("failed to digest a workflow's metadata: %w", err)
		}

		workflowID := workflow.WorkflowSelector.WorkflowID
		if workflowID == "" {
			return fmt.Errorf("node %s reported a workflow with no ID", node)
		}

		if m.reports[workflowID] == nil {
			m.reports[workflowID] = map[string]report{}
		}
		m.reports[workflowID][node] = report{metadata: workflow, digest: digest, at: now}
	}

	m.rebuild(now)
	return nil
}

// Called with the lock held.
func (m *metadata) rebuild(now time.Time) {
	agreed := make(map[string]gateway.WorkflowMetadata, len(m.reports))
	byReference := make(map[reference]string, len(m.reports))

	for workflowID, byNode := range m.reports {
		votes := map[string]int{}
		var winner report

		for _, r := range byNode {
			if now.Sub(r.at) > m.stale {
				continue
			}
			votes[r.digest]++
			if votes[r.digest] >= m.agreement {
				winner = r
			}
		}
		if winner.digest == "" {
			continue
		}

		agreed[workflowID] = winner.metadata
		selector := winner.metadata.WorkflowSelector
		byReference[reference{
			owner: strings.ToLower(selector.WorkflowOwner),
			name:  selector.WorkflowName,
			tag:   selector.WorkflowTag,
		}] = workflowID
	}

	m.agreed, m.byReference = agreed, byReference
}

// Resolve takes either shape of selector: the ID, or the owner, name and tag.
func (m *metadata) Resolve(selector gateway.WorkflowSelector) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if selector.WorkflowID != "" {
		_, known := m.agreed[selector.WorkflowID]
		return selector.WorkflowID, known
	}

	workflowID, known := m.byReference[reference{
		owner: strings.ToLower(selector.WorkflowOwner),
		name:  selector.WorkflowName,
		tag:   selector.WorkflowTag,
	}]
	return workflowID, known
}

// On the address alone: a key is an account, and how it was written down -
// checksummed or not - is not part of who it is.
func (m *metadata) Authorized(workflowID, signer string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	workflow, known := m.agreed[workflowID]
	if !known {
		return false
	}
	for _, key := range workflow.AuthorizedKeys {
		if key.KeyType == gateway.KeyTypeECDSAEVM && strings.EqualFold(key.PublicKey, signer) {
			return true
		}
	}
	return false
}

func (m *metadata) Workflows() map[string]gateway.WorkflowMetadata {
	m.mu.RLock()
	defer m.mu.RUnlock()

	workflows := make(map[string]gateway.WorkflowMetadata, len(m.agreed))
	for id, workflow := range m.agreed {
		workflows[id] = workflow
	}
	return workflows
}

// Expire is what makes a node that stopped talking stop counting.
func (m *metadata) Expire() {
	now := time.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	for workflowID, byNode := range m.reports {
		for node, r := range byNode {
			if now.Sub(r.at) > m.stale {
				delete(byNode, node)
			}
		}
		if len(byNode) == 0 {
			delete(m.reports, workflowID)
		}
	}
	m.rebuild(now)
}
