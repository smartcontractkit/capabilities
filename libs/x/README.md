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
- **`registrysyncer`** — an in-memory snapshot of a CapabilitiesRegistry, the lookups a metadata API
  needs (`LocalNode`, `NodeByPeerID`, `DONsForCapability`, `DONByID`, `ConfigForCapability`), and the
  ORM that stores a snapshot so a restart can answer from the last known registry before its first
  on-chain read lands. Was `core/services/registrysyncer`'s `LocalRegistry` and `orm.go`.

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
- `registrysyncer` drops core's hand-written capability-config decoder for a call to
  chainlink-common's `capabilitiespb.CapabilityConfigFromProto`, which is the same decoder the
  registry's wire path already uses and which states outright that a second one could only drift
  from it. `CapabilityConfiguration.Unmarshal` is kept as core's own call sites spell it, so the
  change is confined to this package.
- `registrysyncer`'s ORM takes its table name, because the processes that keep a registry do not
  share a table, and `AddLocalRegistry` takes a pointer — a `LocalRegistry` carries the mutex
  guarding its local-node cache, so core's by-value signature copied a lock.
- `NodeInfo` loses core's `CapabilitiesDONIds` and `HashedCapabilityIDs`: nothing outside
  `registrysyncer` ever read either, and the vocabulary is now chainlink-common's.

`.golangci.yml` excludes this directory from a few of this repo's rules (including its ban on
go-ethereum), so the code can stay as core wrote it. That exclusion goes away with the package.
