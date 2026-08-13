package registrysyncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/sqlutil"
)

// The registry is read from a chain, and a chain is not always reachable when a process starts. A
// snapshot is therefore persisted after every successful read and loaded once at startup, so a
// restart can answer registry questions immediately instead of failing every lookup until its first
// read lands.
//
// What is stored is the snapshot as JSON, keyed by a hash so an unchanged registry does not write a
// new row. Only the ten most recent rows are kept: this is a cache with an on-chain source of
// truth, not a history anyone reads.

// persistedNodeInfo is the stored form of a NodeInfo.
//
// The JSON keys are pinned here rather than inherited from the Go field names so that renaming a
// field does not silently orphan every row already written. The keys, and the shape of each value,
// are the ones core's registrysyncer already writes, so rows written before this package existed
// still load: Signer and P2pID are ragetypes.PeerID because that is how they were stored, and a
// PeerID marshals as its own string form rather than as 32 numbers.
//
// Fields core did not store (CsaKey, CapabilityIDs) are simply absent from an older row and come
// back zero, which the next sync overwrites.
type persistedNodeInfo struct {
	NodeOperatorID      uint32           `json:"nodeOperatorId"`
	ConfigCount         uint32           `json:"configCount"`
	WorkflowDONID       uint32           `json:"workflowDONId"`
	Signer              ragetypes.PeerID `json:"signer"`
	P2pID               ragetypes.PeerID `json:"p2pId"`
	EncryptionPublicKey [32]byte         `json:"encryptionPublicKey"`
	CsaKey              [32]byte         `json:"csaKey"`
	CapabilityIDs       []string         `json:"capabilityIds"`
}

type persistedRegistry struct {
	IDsToDONs         map[DonID]DON                          `json:"IDsToDONs"`
	IDsToNodes        map[ragetypes.PeerID]persistedNodeInfo `json:"IDsToNodes"`
	IDsToCapabilities map[string]Capability                  `json:"IDsToCapabilities"`
}

func (l *LocalRegistry) MarshalJSON() ([]byte, error) {
	nodes := make(map[ragetypes.PeerID]persistedNodeInfo, len(l.IDsToNodes))
	for id, n := range l.IDsToNodes {
		nodes[id] = persistedNodeInfo{
			NodeOperatorID:      n.NodeOperatorID,
			ConfigCount:         n.ConfigCount,
			WorkflowDONID:       n.WorkflowDONID,
			Signer:              ragetypes.PeerID(n.Signer),
			P2pID:               ragetypes.PeerID(n.P2pID),
			EncryptionPublicKey: n.EncryptionPublicKey,
			CsaKey:              n.CsaKey,
			CapabilityIDs:       n.CapabilityIDs,
		}
	}
	return json.Marshal(&persistedRegistry{
		IDsToDONs:         l.IDsToDONs,
		IDsToNodes:        nodes,
		IDsToCapabilities: l.IDsToCapabilities,
	})
}

// UnmarshalJSON restores a snapshot. Logger and GetPeerID are not stored - neither survives JSON -
// so a restored snapshot is not usable until the caller puts them back.
func (l *LocalRegistry) UnmarshalJSON(data []byte) error {
	stored := persistedRegistry{
		IDsToDONs:         map[DonID]DON{},
		IDsToNodes:        map[ragetypes.PeerID]persistedNodeInfo{},
		IDsToCapabilities: map[string]Capability{},
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("failed to unmarshal registry snapshot: %w", err)
	}

	l.IDsToDONs = stored.IDsToDONs
	l.IDsToCapabilities = stored.IDsToCapabilities
	l.IDsToNodes = make(map[ragetypes.PeerID]NodeInfo, len(stored.IDsToNodes))
	for id, n := range stored.IDsToNodes {
		l.IDsToNodes[id] = NodeInfo{
			NodeOperatorID:      n.NodeOperatorID,
			ConfigCount:         n.ConfigCount,
			WorkflowDONID:       n.WorkflowDONID,
			Signer:              [32]byte(n.Signer),
			P2pID:               [32]byte(n.P2pID),
			EncryptionPublicKey: n.EncryptionPublicKey,
			CsaKey:              n.CsaKey,
			CapabilityIDs:       n.CapabilityIDs,
		}
	}
	return nil
}

// ORM persists registry snapshots.
type ORM interface {
	// AddLocalRegistry stores a snapshot, unless one identical to it is already the latest.
	//
	// By pointer, unlike core's original: a LocalRegistry carries the mutex guarding its local-node
	// cache, so taking one by value copies a lock.
	AddLocalRegistry(ctx context.Context, localRegistry *LocalRegistry) error
	// LatestLocalRegistry returns the most recently stored snapshot. The returned snapshot has no
	// Logger or GetPeerID; see UnmarshalJSON.
	LatestLocalRegistry(ctx context.Context) (*LocalRegistry, error)
}

// safeTableName is what a table this writes to may be called. The name is interpolated into SQL,
// which no amount of parameter binding can do for an identifier, so it is constrained here instead:
// callers pass a constant, and anything that is not a plain identifier is refused outright rather
// than escaped and hoped for.
var safeTableName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

type orm struct {
	ds    sqlutil.DataSource
	lggr  logger.Logger
	table string
}

var _ ORM = (*orm)(nil)

// NewORM returns an ORM storing snapshots in table, which the caller is responsible for having
// migrated into existence. The table is named rather than fixed because the processes that keep a
// registry do not share one: each owns its own table in its own migrations.
//
// Panics if table is not a plain identifier. Callers pass a constant, so a name that fails here is
// a bug in the program rather than a condition it could recover from - the same reason
// regexp.MustCompile panics.
func NewORM(ds sqlutil.DataSource, lggr logger.Logger, table string) ORM {
	if !safeTableName.MatchString(table) {
		panic(fmt.Sprintf("registrysyncer: invalid snapshot table name %q", table))
	}
	return &orm{ds: ds, lggr: logger.Named(lggr, "RegistrySyncerORM"), table: table}
}

func (o *orm) AddLocalRegistry(ctx context.Context, localRegistry *LocalRegistry) error {
	return sqlutil.TransactDataSource(ctx, o.ds, nil, func(tx sqlutil.DataSource) error {
		snapshot, err := localRegistry.MarshalJSON()
		if err != nil {
			return err
		}
		hash := sha256.Sum256(snapshot)

		// Insert only when the newest row is not already this exact snapshot: the registry changes
		// far less often than it is read, so most syncs have nothing new to store.
		r, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (data, data_hash)
            SELECT $1, $2
            WHERE $2 NOT IN (
                SELECT data_hash FROM %s
                ORDER BY id DESC LIMIT 1
            )`, o.table, o.table), snapshot, hex.EncodeToString(hash[:]))
		if err != nil {
			return fmt.Errorf("failed to insert into %s: %w", o.table, err)
		}
		if n, _ := r.RowsAffected(); n == 0 {
			o.lggr.Debugw("registry snapshot unchanged, nothing stored", "hash", hex.EncodeToString(hash[:]))
			return nil
		}
		o.lggr.Debugw("stored a new registry snapshot", "hash", hex.EncodeToString(hash[:]))

		_, err = tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s
WHERE data_hash NOT IN (
    SELECT data_hash FROM %s
    ORDER BY id DESC
    LIMIT 10
)`, o.table, o.table))
		return err
	})
}

func (o *orm) LatestLocalRegistry(ctx context.Context) (*LocalRegistry, error) {
	var snapshot string
	err := o.ds.GetContext(ctx, &snapshot,
		fmt.Sprintf(`SELECT data FROM %s ORDER BY id DESC LIMIT 1`, o.table))
	if err != nil {
		return nil, err
	}

	var localRegistry LocalRegistry
	if err := localRegistry.UnmarshalJSON([]byte(snapshot)); err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(snapshot))
	o.lggr.Debugw("restored a registry snapshot", "hash", hex.EncodeToString(hash[:]))
	return &localRegistry, nil
}
