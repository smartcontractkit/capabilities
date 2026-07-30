package trigger

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
)

// physicalFilterID returns the workflow-independent content identity of an EVM
// log filter: the lowercase hex SHA-256 over a canonical encoding of the
// filter's physical matching criteria (chain selector, addresses, event
// signatures, and positional topic slots). Two filters that match exactly the
// same on-chain logs hash to the same ID regardless of which workflow or
// trigger registered them, or of the order their addresses/sigs/topics were
// supplied. It is used as ResourceIdentity.ResourceID and as the
// RESERVE/RELEASE event identity so identical filters share one billable
// physical resource (R4).
//
// Canonicalization rules (each rule defeats a source of non-determinism):
//   - addresses and event sigs are lowercased 0x-prefixed hex and sorted
//     ascending: the matching set is order-independent;
//   - topic2/topic3/topic4 are POSITIONAL — a value in topic2 is a different
//     filter than the same value in topic3 — so each slot is encoded under its
//     own positional tag, and within a slot the values are sorted ascending;
//   - the chain selector scopes the hash so identical filters on different
//     chains stay distinct.
//
// The preimage uses "|" as a top-level separator and "," within a set; the
// per-element hex encodings are fixed-width and contain neither, so the
// encoding is unambiguous.
func physicalFilterID(chainSelector string, addresses []evmtypes.Address, eventSigs, topic2, topic3, topic4 []evmtypes.Hash) string {
	sortedAddrs := make([]string, len(addresses))
	for i, a := range addresses {
		sortedAddrs[i] = "0x" + hex.EncodeToString(a[:])
	}
	sort.Strings(sortedAddrs)

	canonHashes := func(hs []evmtypes.Hash) string {
		out := make([]string, len(hs))
		for i, h := range hs {
			out[i] = "0x" + hex.EncodeToString(h[:])
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}

	// Topic slots are encoded positionally so the same value in different slots
	// produces a different identity.
	preimage := strings.Join([]string{
		"cs=" + chainSelector,
		"addrs=" + strings.Join(sortedAddrs, ","),
		"sigs=" + canonHashes(eventSigs),
		"t2=" + canonHashes(topic2),
		"t3=" + canonHashes(topic3),
		"t4=" + canonHashes(topic4),
	}, "|")

	sum := sha256.Sum256([]byte(preimage))
	return hex.EncodeToString(sum[:])
}
