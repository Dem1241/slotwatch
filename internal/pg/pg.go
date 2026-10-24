// Package pg reads replication slots and the settings that decide how much
// WAL they can hold on to. It only ever runs SELECTs.
package pg

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Slot is one row of pg_replication_slots, with the WAL it holds back worked
// out against the server's current position.
type Slot struct {
	Name      string
	Type      string // "logical" or "physical"
	Plugin    string // output plugin of a logical slot, like pgoutput
	Database  string
	Temporary bool
	Active    bool
	ActivePID int

	// Retained is how much WAL the server keeps for this slot, from its
	// restart_lsn. Lag is how far behind the consumer has confirmed, from
	// confirmed_flush_lsn (logical slots only, -1 otherwise).
	Retained int64
	Lag      int64

	// WALStatus is reserved, extended, unreserved or lost (PostgreSQL 13+).
	// SafeWALSize is how much more WAL can be written before the slot is in
	// danger of losing it, or -1 when there is no limit.
	WALStatus   string
	SafeWALSize int64

	// InactiveSince is when the slot was last released (PostgreSQL 17+).
	InactiveSince time.Time
	// Invalidated says why the server gave up on the slot, if it did.
	Invalidated string

	// Client is who holds the slot right now, from pg_stat_replication.
	Client string
}

// Server is what the slots are measured against.
type Server struct {
	Version    int // server_version_num, like 170002
	InRecovery bool
	WALLevel   string
	// MaxSlotWALKeep is max_slot_wal_keep_size in bytes, or -1 for no limit.
	MaxSlotWALKeep int64
	MaxSlots       int
	Name           string // host and database, for the header
}

// Snapshot is everything slotwatch knows at one moment.
type Snapshot struct {
	At     time.Time
	Server Server
	Slots  []Slot
}

// Client reads snapshots from one PostgreSQL server.
type Client struct {
	conn   *pgx.Conn
	server Server
	query  string
}

// Connect opens a connection. An empty dsn uses the usual PGHOST, PGUSER and
// friends, like psql does.
func Connect(ctx context.Context, dsn string) (*Client, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn}
	if err := c.loadServer(ctx); err != nil {
		_ = conn.Close(ctx)
		return nil, err
	}
	c.query = slotQuery(c.server.Version)
	return c, nil
}

// Close closes the connection.
func (c *Client) Close() { _ = c.conn.Close(context.Background()) }

func (c *Client) loadServer(ctx context.Context) error {
	var version, maxSlots, keep string
	err := c.conn.QueryRow(ctx, `
		SELECT current_setting('server_version_num'),
		       pg_is_in_recovery(),
		       current_setting('wal_level'),
		       current_setting('max_replication_slots'),
		       coalesce(current_setting('max_slot_wal_keep_size', true), '-1')`,
	).Scan(&version, &c.server.InRecovery, &c.server.WALLevel, &maxSlots, &keep)
	if err != nil {
		return fmt.Errorf("reading server settings: %w", err)
	}
	c.server.Version, _ = strconv.Atoi(version)
	c.server.MaxSlots, _ = strconv.Atoi(maxSlots)
	c.server.MaxSlotWALKeep = parseMB(keep)

	cfg := c.conn.Config()
	c.server.Name = cfg.Host + "/" + cfg.Database
	if c.server.Version < 120000 {
		return fmt.Errorf("postgresql %s is too old, slotwatch needs 12 or newer", version)
	}
	return nil
}

// parseMB turns max_slot_wal_keep_size, which current_setting returns in
// megabytes or with a unit like 10GB, into bytes. -1 means no limit.
func parseMB(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-1" {
		return -1
	}
	units := []struct {
		suffix string
		mult   int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"kB", 1 << 10}}
	for _, u := range units {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
			if err != nil {
				return -1
			}
			return v * u.mult
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1
	}
	return v << 20
}

// slotQuery builds the slot query for a server version, leaving out the
// columns it doesn't have yet.
func slotQuery(version int) string {
	walStatus, safeWAL := "''", "NULL::bigint"
	if version >= 130000 {
		walStatus, safeWAL = "coalesce(s.wal_status, '')", "s.safe_wal_size"
	}
	inactiveSince, invalidated := "NULL::timestamptz", "''"
	if version >= 170000 {
		inactiveSince = "s.inactive_since"
		invalidated = "coalesce(s.invalidation_reason, '')"
	} else if version >= 160000 {
		invalidated = "CASE WHEN s.conflicting THEN 'conflicting' ELSE '' END"
	}
	// on a standby the position to compare against is what has been replayed
	return fmt.Sprintf(`
		WITH pos AS (
			SELECT CASE WHEN pg_is_in_recovery() THEN pg_last_wal_replay_lsn() ELSE pg_current_wal_lsn() END AS lsn
		)
		SELECT s.slot_name,
		       s.slot_type,
		       coalesce(s.plugin, ''),
		       coalesce(s.database, ''),
		       s.temporary,
		       s.active,
		       coalesce(s.active_pid, 0),
		       coalesce(pg_wal_lsn_diff(pos.lsn, s.restart_lsn), 0)::bigint,
		       coalesce(pg_wal_lsn_diff(pos.lsn, s.confirmed_flush_lsn), -1)::bigint,
		       %s,
		       coalesce(%s, -1),
		       %s,
		       %s,
		       coalesce(nullif(r.application_name, ''), host(r.client_addr), '')
		FROM pg_replication_slots s
		CROSS JOIN pos
		LEFT JOIN pg_stat_replication r ON r.pid = s.active_pid
		ORDER BY s.slot_name`,
		walStatus, safeWAL, inactiveSince, invalidated)
}

// Snapshot reads all slots.
func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	rows, err := c.conn.Query(ctx, c.query)
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading replication slots: %w", err)
	}
	defer rows.Close()

	snap := Snapshot{At: time.Now(), Server: c.server}
	for rows.Next() {
		var s Slot
		var inactiveSince *time.Time
		if err := rows.Scan(&s.Name, &s.Type, &s.Plugin, &s.Database, &s.Temporary, &s.Active, &s.ActivePID,
			&s.Retained, &s.Lag, &s.WALStatus, &s.SafeWALSize, &inactiveSince, &s.Invalidated, &s.Client); err != nil {
			return Snapshot{}, fmt.Errorf("reading replication slots: %w", err)
		}
		if inactiveSince != nil {
			s.InactiveSince = *inactiveSince
		}
		snap.Slots = append(snap.Slots, s)
	}
	if err := rows.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("reading replication slots: %w", err)
	}
	return snap, nil
}
