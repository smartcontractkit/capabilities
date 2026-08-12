# libs/x

Code that is passing through on its way somewhere else. Nothing here is a stable API, and nothing
outside `crecore` and chainlink's core should import it.

Today it holds DON-to-DON communication, moved out of chainlink core so that core and crecore can
build against the same code while crecore takes ownership of it:

- **`rage`** — the rage p2p transport: a peer, the shared peer that keeps a group and a stream per
  remote node, and the types they exchange. Was `core/services/p2p` (+ `core/services/p2p/types`).
- **`don2don`** — everything above the transport: signing, validating and routing messages
  (`dispatcher.go`), fanning a call out to every member of a capability DON and agreeing on the
  responses (`executable/`), the same in the other direction for triggers, and the transmission
  schedule that staggers who is asked when. Was `core/capabilities/remote` (+
  `core/capabilities/transmission` and `core/capabilities/validation`).

The end state is that crecore owns DON-to-DON outright: a new process talks to another DON by asking
crecore, rather than by running a peer and a dispatcher of its own. When core no longer uses any of
this, it moves into crecore and stops being importable at all.

The code is kept as close to its origin as possible so the move stays reviewable as a move. What
changed:

- The dispatcher's configuration is a struct (`don2don.DispatcherConfig`) instead of an interface
  onto a node's TOML.
- `rage.SharedPeer` takes a `PeerSource` — a group factory and a peer ID, read at start — instead of
  core's `SingletonPeerWrapper`.
- Two small helpers came along rather than dragging their old homes with them:
  `rage.NewSignerPeerKeyring` (was in core's `ocrcommon`) and `rage.MustNewPeerID` (was in core's
  `utils`).
- The wire protos keep their proto package (`remote`) and field numbers, so only the Go import path
  moved. Nothing may generate a second copy: two registrations of `remote.MessageBody` in one binary
  panic at init.

`.golangci.yml` excludes this directory from a few of this repo's rules (including its ban on
go-ethereum), so the code can stay as core wrote it. That exclusion goes away with the package.
