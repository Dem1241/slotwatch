package pg

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseMB(t *testing.T) {
	tests := map[string]int64{
		"-1":     -1,
		"":       -1,
		"1024":   1 << 30,
		"512MB":  512 << 20,
		"10GB":   10 << 30,
		"2TB":    2 << 40,
		"64kB":   64 << 10,
		"lots":   -1,
		" 50GB ": 50 << 30,
	}
	for s, want := range tests {
		if got := parseMB(s); got != want {
			t.Errorf("parseMB(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestSlotQueryFollowsTheServerVersion(t *testing.T) {
	tests := []struct {
		version int
		has     []string
		hasNot  []string
	}{
		{120000, nil, []string{"wal_status", "inactive_since", "conflicting", "invalidation_reason"}},
		{130000, []string{"s.wal_status", "s.safe_wal_size"}, []string{"inactive_since", "conflicting"}},
		{160000, []string{"s.wal_status", "s.conflicting"}, []string{"inactive_since", "invalidation_reason"}},
		{170000, []string{"s.wal_status", "s.inactive_since", "s.invalidation_reason"}, []string{"conflicting"}},
	}
	for _, tt := range tests {
		q := slotQuery(tt.version)
		for _, s := range tt.has {
			if !strings.Contains(q, s) {
				t.Errorf("query for %d doesn't use %s", tt.version, s)
			}
		}
		for _, s := range tt.hasNot {
			if strings.Contains(q, s) {
				t.Errorf("query for %d uses %s, which that version doesn't have", tt.version, s)
			}
		}
	}
}

// TestSnapshot runs against a real server when SLOTWATCH_TEST_DSN is set. The
// server needs wal_level=logical.
func TestSnapshot(t *testing.T) {
	dsn := os.Getenv("SLOTWATCH_TEST_DSN")
	if dsn == "" {
		t.Skip("set SLOTWATCH_TEST_DSN to run against a real server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// registered first, so it runs after the cleanups below that still need it
	t.Cleanup(c.Close)

	const name = "slotwatch_test"
	if _, err := c.conn.Exec(ctx, `SELECT pg_create_logical_replication_slot($1, 'test_decoding')`, name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.conn.Exec(context.Background(), `SELECT pg_drop_replication_slot($1)`, name) })

	// write a little WAL for the slot to hold on to
	if _, err := c.conn.Exec(ctx, `CREATE TABLE slotwatch_wal AS SELECT g FROM generate_series(1, 20000) g`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.conn.Exec(context.Background(), `DROP TABLE slotwatch_wal`) })

	snap, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Server.Version < 120000 || snap.Server.WALLevel != "logical" {
		t.Errorf("server = %+v", snap.Server)
	}
	var found bool
	for _, s := range snap.Slots {
		if s.Name != name {
			continue
		}
		found = true
		if s.Type != "logical" || s.Plugin != "test_decoding" || s.Active {
			t.Errorf("slot = %+v", s)
		}
		if s.Retained <= 0 || s.Lag <= 0 {
			t.Errorf("retained %d and lag %d, want both above 0 after writing WAL", s.Retained, s.Lag)
		}
		if snap.Server.Version >= 130000 && s.WALStatus == "" {
			t.Error("wal_status is empty")
		}
		if snap.Server.Version >= 170000 && s.InactiveSince.IsZero() {
			t.Error("inactive_since is empty")
		}
	}
	if !found {
		t.Errorf("slot %s isn't in the snapshot", name)
	}
}
