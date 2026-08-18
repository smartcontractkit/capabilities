// Package ocr holds the pieces of an OCR oracle that are not a node's and not a
// chain's, so that a capability can run one wherever it is hosted.
package ocr

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// Database is the libocr database of an OCR-based capability, held in memory.
//
// Nothing it holds outlives the process on purpose. The config is a cache, and a
// miss is answered by the config tracker rather than being an error. The
// protocol states are progress within one config digest, which an oracle
// restarting rejoins from the current config anyway. The pending transmissions
// are reports waiting to be delivered, and a capability delivers them to
// whoever called it, in this process - so one lost with the process is a request
// whose caller is no longer waiting for it either.
//
// That last point is what makes this safe here and not in general: an OCR job
// that writes to a chain must survive a restart still owing that write, and uses
// a durable database for exactly that reason.
//
// Capabilities are the only thing that runs on this. In the node it backs the
// one oracle factory the standard capabilities delegate builds, and nothing
// else: the OCR2 plugins that write to chains each bring their own durable
// database. So this lives here rather than in chainlink-common - it is not a
// general-purpose OCR database, and holding it in memory is a statement about
// capabilities rather than about OCR.
type Database struct {
	// name identifies this oracle in errors, since a capability can run more
	// than one.
	name                 string
	lggr                 logger.SugaredLogger
	config               *ocrtypes.ContractConfig
	states               map[ocrtypes.ConfigDigest]*ocrtypes.PersistentState
	pendingTransmissions map[ocrtypes.ReportTimestamp]ocrtypes.PendingTransmission
	protocolStates       map[ocrtypes.ConfigDigest]map[string][]byte

	mu sync.Mutex
}

// Both, deliberately: the states, config and pending transmissions are the
// database every protocol from OCR2 on keeps, and the protocol state is what
// OCR3 adds on top of it. Nothing here is particular to either.
var (
	_ ocrtypes.Database  = &Database{}
	_ ocr3types.Database = &Database{}
)

// NewDatabase returns the database of the oracle named name.
func NewDatabase(name string, lggr logger.Logger) *Database {
	return &Database{
		name:                 name,
		lggr:                 logger.Sugared(logger.Named(lggr, "OracleMemoryDB")),
		states:               make(map[ocrtypes.ConfigDigest]*ocrtypes.PersistentState),
		pendingTransmissions: make(map[ocrtypes.ReportTimestamp]ocrtypes.PendingTransmission),
		protocolStates:       make(map[ocrtypes.ConfigDigest]map[string][]byte),
	}
}

func (d *Database) ReadState(ctx context.Context, cd ocrtypes.ConfigDigest) (*ocrtypes.PersistentState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	ps, ok := d.states[cd]
	if !ok {
		return nil, fmt.Errorf("state not found for oracle %s, config digest %s", d.name, cd)
	}

	return ps, nil
}

func (d *Database) WriteState(ctx context.Context, cd ocrtypes.ConfigDigest, state ocrtypes.PersistentState) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.states[cd] = &state
	return nil
}

// ReadConfig returns nil, nil when there is no config yet: that is a cache miss
// rather than a failure, and the caller resolves it from the config tracker.
func (d *Database) ReadConfig(ctx context.Context) (*ocrtypes.ContractConfig, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.config == nil {
		return nil, nil
	}
	return d.config, nil
}

func (d *Database) WriteConfig(ctx context.Context, c ocrtypes.ContractConfig) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.config = &c
	d.lggr.Debugw("Wrote config", "oracle", d.name, "configDigest", c.ConfigDigest, "configCount", c.ConfigCount)

	return nil
}

func (d *Database) StorePendingTransmission(ctx context.Context, t ocrtypes.ReportTimestamp, tx ocrtypes.PendingTransmission) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.pendingTransmissions[t] = tx
	return nil
}

func (d *Database) PendingTransmissionsWithConfigDigest(ctx context.Context, cd ocrtypes.ConfigDigest) (map[ocrtypes.ReportTimestamp]ocrtypes.PendingTransmission, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	m := make(map[ocrtypes.ReportTimestamp]ocrtypes.PendingTransmission)
	for k, v := range d.pendingTransmissions {
		if k.ConfigDigest == cd {
			m[k] = v
		}
	}

	return m, nil
}

func (d *Database) DeletePendingTransmission(ctx context.Context, t ocrtypes.ReportTimestamp) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.pendingTransmissions, t)
	return nil
}

func (d *Database) DeletePendingTransmissionsOlderThan(ctx context.Context, t time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	for k, v := range d.pendingTransmissions {
		if v.Time.Before(t) {
			delete(d.pendingTransmissions, k)
		}
	}

	return nil
}

// ReadProtocolState returns nil, nil for a key that was never written, which is
// how libocr asks whether there is any.
func (d *Database) ReadProtocolState(ctx context.Context, configDigest ocrtypes.ConfigDigest, key string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	value, ok := d.protocolStates[configDigest][key]
	if !ok {
		return nil, nil
	}
	return value, nil
}

// WriteProtocolState writes value, or deletes the key when value is nil.
func (d *Database) WriteProtocolState(ctx context.Context, configDigest ocrtypes.ConfigDigest, key string, value []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if value == nil {
		delete(d.protocolStates[configDigest], key)
		return nil
	}

	if d.protocolStates[configDigest] == nil {
		d.protocolStates[configDigest] = make(map[string][]byte)
	}
	d.protocolStates[configDigest][key] = value
	return nil
}
