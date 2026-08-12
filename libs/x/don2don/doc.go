// Package don2don is DON-to-DON communication: sending a request to every member of another DON,
// collecting their answers, and agreeing on one.
//
// The pieces, from the bottom up:
//
//   - dispatcher.go signs, validates and routes messages. It is the seam the rest of this package
//     sits on: everything above it says "send this to that peer ID" and knows nothing about how.
//   - executable/ turns one call into a request to each member of the capability DON, counts
//     identical responses until F+1 agree, and fails early once agreement has become impossible.
//     Its mirror on the receiving side waits for a quorum of callers before doing any work.
//   - trigger_publisher.go and trigger_subscriber.go do the same in the other direction, for
//     capabilities that emit rather than answer.
//   - transmission/ decides who is asked when, so a DON does not stampede a capability.
//
// None of it is tied to rage. A dispatcher needs a transport that can send bytes to a peer ID and
// deliver bytes back with the sender's identity attached; today that is a rage peer (see libs/x/rage),
// and it could as easily be direct RPC between processes that already know each other's addresses.
// What holds the guarantees up is the signature on every message body, not the transport underneath.
//
// It lives under libs/x because it is on its way somewhere else. crecore is meant to own DON-to-DON
// communication outright, so that a new process talks to another DON by asking crecore rather than
// by running any of this itself. When core no longer uses it, this moves into crecore and stops
// being importable. Until then it is here so core and crecore build against the same code, and
// nothing else should import it.
//
// Moved from chainlink's core/capabilities/remote (plus core/capabilities/transmission and
// core/capabilities/validation). Two things changed on the way: the dispatcher's configuration is a
// struct rather than an interface onto a node's TOML (see config.go), and peer types come from
// libs/x/rage rather than core/services/p2p/types.
package don2don
