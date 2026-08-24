// Package eventstore is where a trigger capability keeps the events it has sent
// but not had acknowledged.
//
// A trigger fires into a workflow that may not have run yet, may be restarting,
// or may never answer; the base trigger retransmits until it is acknowledged, and
// this is what it retransmits from. Holding that in memory would mean a restart
// dropping every event in flight, which is exactly when they matter - so it is a
// table, in the database the capability already has.
//
// The DDL is the capability's, not this package's: a binary owns its migrations,
// and this states what those migrations have to have made. See Schema.
package eventstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/sqlutil"
)

// Table is where the events are kept, unqualified so that the connection's search
// path decides which schema that is - the same rule the rest of a capability's
// tables follow, and what keeps the instances of an embedded run from answering
// each other's events.
const Table = "trigger_pending_events"

// Schema is the DDL this package expects, for a capability's migrations to
// include verbatim.
//
// It is a constant rather than a migration of its own because migrations belong
// to a binary: one goose history per database, owned by whoever runs it. What
// this package can do is say what shape it reads.
const Schema = `CREATE TABLE IF NOT EXISTS ` + Table + ` (
	scope        TEXT        NOT NULL DEFAULT '',
	trigger_id   TEXT        NOT NULL,
	event_id     TEXT        NOT NULL,
	payload      BYTEA       NOT NULL,
	first_at     TIMESTAMPTZ NOT NULL,
	last_sent_at TIMESTAMPTZ NULL,
	attempts     INTEGER     NOT NULL DEFAULT 0,
	org_id       TEXT        NOT NULL DEFAULT '',
	PRIMARY KEY (scope, trigger_id, event_id)
)`

// New returns the event store over ds, holding the events of one scope.
//
// A scope is whoever these events are owed to when the table is shared: two
// processes of the same capability on one schema - the EVM capability on two
// chains, say - would otherwise list, retransmit and delete each other's events,
// since a trigger ID says which workflow asked but not which chain it asked of. A
// capability with a table to itself passes "" and never thinks about it again.
func New(ds sqlutil.DataSource, scope string) capabilities.EventStore {
	return &store{ds: ds, scope: scope}
}

type store struct {
	ds    sqlutil.DataSource
	scope string
}

var _ capabilities.EventStore = (*store)(nil)

func (s *store) Insert(ctx context.Context, rec capabilities.PendingEvent) error {
	const q = `INSERT INTO ` + Table + ` (scope, trigger_id, event_id, payload, first_at, last_sent_at, attempts, org_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	var lastSent sql.NullTime
	if !rec.LastSentAt.IsZero() {
		lastSent = sql.NullTime{Time: rec.LastSentAt, Valid: true}
	}

	if _, err := s.ds.ExecContext(ctx, q,
		s.scope, rec.TriggerId, rec.EventId, rec.Payload, rec.FirstAt, lastSent, rec.Attempts, rec.OrgID,
	); err != nil {
		return fmt.Errorf("failed to insert pending event trigger_id=%s event_id=%s: %w", rec.TriggerId, rec.EventId, err)
	}
	return nil
}

// UpdateDelivery records another attempt at an event.
//
// An event that is no longer there answers with sql.ErrNoRows rather than
// silently doing nothing: it was acknowledged while this send was in flight, and
// the caller is entitled to know the difference between that and a write that
// landed.
func (s *store) UpdateDelivery(ctx context.Context, triggerID, eventID string, lastSentAt time.Time, attempts int) error {
	const q = `UPDATE ` + Table + `
SET last_sent_at = $4, attempts = $5
WHERE scope = $1 AND trigger_id = $2 AND event_id = $3`

	var lastSent any
	if !lastSentAt.IsZero() {
		lastSent = lastSentAt
	}

	res, err := s.ds.ExecContext(ctx, q, s.scope, triggerID, eventID, lastSent, attempts)
	if err != nil {
		return fmt.Errorf("failed to update delivery for trigger_id=%s event_id=%s: %w", triggerID, eventID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected updating delivery for trigger_id=%s event_id=%s: %w", triggerID, eventID, err)
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// List returns everything still unacknowledged, oldest first, which is the order
// a restart replays them in.
func (s *store) List(ctx context.Context) ([]capabilities.PendingEvent, error) {
	const q = `SELECT trigger_id, event_id, payload, first_at, last_sent_at, attempts, org_id
FROM ` + Table + `
WHERE scope = $1
ORDER BY first_at ASC`

	type row struct {
		TriggerID  string       `db:"trigger_id"`
		EventID    string       `db:"event_id"`
		Payload    []byte       `db:"payload"`
		FirstAt    time.Time    `db:"first_at"`
		LastSentAt sql.NullTime `db:"last_sent_at"`
		Attempts   int          `db:"attempts"`
		OrgID      string       `db:"org_id"`
	}

	var rows []row
	if err := s.ds.SelectContext(ctx, &rows, q, s.scope); err != nil {
		return nil, fmt.Errorf("failed to list pending events: %w", err)
	}

	events := make([]capabilities.PendingEvent, 0, len(rows))
	for _, r := range rows {
		var lastSent time.Time
		if r.LastSentAt.Valid {
			lastSent = r.LastSentAt.Time
		}
		events = append(events, capabilities.PendingEvent{
			TriggerId:  r.TriggerID,
			EventId:    r.EventID,
			Payload:    append([]byte(nil), r.Payload...),
			FirstAt:    r.FirstAt,
			LastSentAt: lastSent,
			Attempts:   r.Attempts,
			OrgID:      r.OrgID,
		})
	}
	return events, nil
}

// DeleteEvent forgets one event, which is what an acknowledgement means.
func (s *store) DeleteEvent(ctx context.Context, triggerID, eventID string) error {
	const q = `DELETE FROM ` + Table + ` WHERE scope = $1 AND trigger_id = $2 AND event_id = $3`
	if _, err := s.ds.ExecContext(ctx, q, s.scope, triggerID, eventID); err != nil {
		return fmt.Errorf("failed to delete pending event trigger_id=%s event_id=%s: %w", triggerID, eventID, err)
	}
	return nil
}

// DeleteEventsForTrigger forgets everything owed to a trigger, which is what
// unregistering it means: nothing is waiting for those events any more.
func (s *store) DeleteEventsForTrigger(ctx context.Context, triggerID string) error {
	const q = `DELETE FROM ` + Table + ` WHERE scope = $1 AND trigger_id = $2`
	if _, err := s.ds.ExecContext(ctx, q, s.scope, triggerID); err != nil {
		return fmt.Errorf("failed to delete pending events for trigger_id=%s: %w", triggerID, err)
	}
	return nil
}
