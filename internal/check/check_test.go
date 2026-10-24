package check

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Dem1241/slotwatch/internal/pg"
	"github.com/Dem1241/slotwatch/internal/slots"
)

func statuses(sl ...pg.Slot) (pg.Snapshot, []slots.Status) {
	snap := pg.Snapshot{
		At:     time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
		Server: pg.Server{Name: "db/shop", Version: 170002, MaxSlotWALKeep: -1},
		Slots:  sl,
	}
	return snap, slots.NewTracker(slots.DefaultLimits).Update(snap)
}

func TestCode(t *testing.T) {
	tests := []struct {
		name string
		slot pg.Slot
		want int
	}{
		{"ok", pg.Slot{Name: "a", Active: true}, ExitOK},
		{"warn", pg.Slot{Name: "a", Active: true, Retained: 2 << 30}, ExitWarn},
		{"danger", pg.Slot{Name: "a", WALStatus: "lost"}, ExitDanger},
	}
	for _, tt := range tests {
		_, sts := statuses(tt.slot, pg.Slot{Name: "fine", Active: true})
		if got := Code(sts); got != tt.want {
			t.Errorf("%s: code = %d, want %d", tt.name, got, tt.want)
		}
	}
	if got := Code(nil); got != ExitOK {
		t.Errorf("no slots: code = %d, want %d", got, ExitOK)
	}
}

func TestText(t *testing.T) {
	snap, sts := statuses(
		pg.Slot{Name: "debezium_shop", Type: "logical", Plugin: "pgoutput", Retained: 3 << 30, WALStatus: "extended"},
		pg.Slot{Name: "replica", Type: "physical", Active: true, Retained: 16 << 20, WALStatus: "reserved"},
	)
	var b bytes.Buffer
	if err := Text(&b, snap, sts); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"LEVEL", "debezium_shop", "logical/pgoutput", "3.0 GB", "warn debezium_shop", "holding 3.0 GB of WAL", "replica", "active"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ok replica\n") {
		t.Errorf("an ok slot got a reasons section:\n%s", out)
	}
}

func TestTextWithoutSlots(t *testing.T) {
	snap, sts := statuses()
	var b bytes.Buffer
	if err := Text(&b, snap, sts); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "no replication slots on db/shop\n" {
		t.Errorf("report = %q", got)
	}
}

func TestJSON(t *testing.T) {
	snap, sts := statuses(
		pg.Slot{Name: "lost_one", Type: "logical", WALStatus: "lost", Invalidated: "wal_removed", Lag: -1},
		pg.Slot{Name: "replica", Type: "physical", Active: true, Retained: 1 << 20, Lag: -1},
	)
	var b bytes.Buffer
	if err := JSON(&b, snap, sts); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Level string `json:"level"`
		Slots []struct {
			Name     string   `json:"name"`
			Level    string   `json:"level"`
			Reasons  []string `json:"reasons"`
			LagBytes *int64   `json:"lag_bytes"`
		} `json:"slots"`
	}
	if err := json.Unmarshal(b.Bytes(), &out); err != nil {
		t.Fatalf("not json: %v\n%s", err, b.String())
	}
	if out.Level != "danger" || len(out.Slots) != 2 {
		t.Fatalf("got %+v", out)
	}
	if s := out.Slots[0]; s.Name != "lost_one" || s.Level != "danger" || len(s.Reasons) == 0 {
		t.Errorf("first slot = %+v", s)
	}
	if s := out.Slots[1]; s.Reasons == nil || s.LagBytes != nil {
		t.Errorf("an ok physical slot should have empty reasons and no lag, got %+v", s)
	}
}
