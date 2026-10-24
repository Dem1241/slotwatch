package slots

import (
	"strings"
	"testing"
	"time"

	"github.com/Dem1241/slotwatch/internal/pg"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func snap(at time.Time, keep int64, s ...pg.Slot) pg.Snapshot {
	return pg.Snapshot{At: at, Server: pg.Server{MaxSlotWALKeep: keep}, Slots: s}
}

func only(t *testing.T, sts []Status) Status {
	t.Helper()
	if len(sts) != 1 {
		t.Fatalf("got %d statuses, want 1", len(sts))
	}
	return sts[0]
}

func hasReason(st Status, part string) bool {
	for _, r := range st.Reasons {
		if strings.Contains(r, part) {
			return true
		}
	}
	return false
}

func TestLevels(t *testing.T) {
	tests := []struct {
		name   string
		slot   pg.Slot
		keep   int64
		want   Level
		reason string
	}{
		{"healthy active slot", pg.Slot{Name: "a", Active: true, Retained: 10 << 20}, 20 << 30, OK, ""},
		{"lots of wal", pg.Slot{Name: "a", Active: true, Retained: 2 << 30}, 20 << 30, Warn, "holding 2.0 GB"},
		{"too much wal", pg.Slot{Name: "a", Active: true, Retained: 12 << 30}, 20 << 30, Danger, "holding 12.0 GB"},
		{"lost", pg.Slot{Name: "a", WALStatus: "lost"}, 20 << 30, Danger, "is gone"},
		{"unreserved", pg.Slot{Name: "a", Active: true, WALStatus: "unreserved"}, 20 << 30, Danger, "next checkpoint"},
		{"invalidated", pg.Slot{Name: "a", Invalidated: "wal_removed"}, 20 << 30, Danger, "invalidated: wal_removed"},
		{"extended", pg.Slot{Name: "a", Active: true, WALStatus: "extended"}, 20 << 30, Warn, "past max_wal_size"},
		{"idle according to the server", pg.Slot{Name: "a", InactiveSince: t0.Add(-time.Hour)}, 20 << 30, Warn, "unused for 1h 0m"},
		{"idle for a moment", pg.Slot{Name: "a", InactiveSince: t0.Add(-time.Minute)}, 20 << 30, OK, ""},
		{"no limit is mentioned", pg.Slot{Name: "a", Active: true, Retained: 2 << 30}, -1, Warn, "grows until the disk is full"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := only(t, NewTracker(DefaultLimits).Update(snap(t0, tt.keep, tt.slot)))
			if st.Level != tt.want {
				t.Errorf("level = %s, want %s (reasons %q)", st.Level, tt.want, st.Reasons)
			}
			if tt.reason != "" && !hasReason(st, tt.reason) {
				t.Errorf("reasons %q don't mention %q", st.Reasons, tt.reason)
			}
			if tt.want == OK && len(st.Reasons) > 0 {
				t.Errorf("an ok slot has reasons %q", st.Reasons)
			}
		})
	}
}

func TestInactiveTrackedWhenTheServerDoesNotSay(t *testing.T) {
	tr := NewTracker(DefaultLimits)
	idle := pg.Slot{Name: "a"}
	tr.Update(snap(t0, -1, idle))
	st := only(t, tr.Update(snap(t0.Add(15*time.Minute), -1, idle)))
	if st.InactiveFor != 15*time.Minute || st.Level != Warn {
		t.Errorf("inactive for %s at %s, want 15m at warn", st.InactiveFor, st.Level)
	}

	// coming back resets the clock
	tr.Update(snap(t0.Add(16*time.Minute), -1, pg.Slot{Name: "a", Active: true}))
	st = only(t, tr.Update(snap(t0.Add(17*time.Minute), -1, idle)))
	if st.InactiveFor != 0 {
		t.Errorf("inactive for %s after the slot was used again, want 0", st.InactiveFor)
	}
}

func TestRateAndTimeToLimit(t *testing.T) {
	tr := NewTracker(DefaultLimits)
	const limit = 20 << 30
	var st Status
	// 1 MB a second for a minute, measured every 10 seconds
	for i := 0; i <= 6; i++ {
		st = only(t, tr.Update(snap(t0.Add(time.Duration(i)*10*time.Second), limit,
			pg.Slot{Name: "a", Active: true, Retained: int64(i) * 10 << 20})))
	}
	if st.Rate != 1<<20 {
		t.Errorf("rate = %f bytes/s, want %d", st.Rate, 1<<20)
	}
	if want := time.Duration(float64(limit-60<<20) / (1 << 20) * float64(time.Second)); st.UntilLimit != want {
		t.Errorf("until limit = %s, want %s", st.UntilLimit, want)
	}
	if len(st.History) != 7 {
		t.Errorf("history has %d points, want 7", len(st.History))
	}
}

func TestCloseToTheLimitIsDanger(t *testing.T) {
	tr := NewTracker(Limits{Warn: 100 << 30, Danger: 200 << 30, Inactive: time.Hour})
	const limit = 1 << 30
	tr.Update(snap(t0, limit, pg.Slot{Name: "a", Active: true, Retained: 500 << 20}))
	st := only(t, tr.Update(snap(t0.Add(time.Minute), limit, pg.Slot{Name: "a", Active: true, Retained: 600 << 20})))
	if st.Level != Danger || !hasReason(st, "reaches max_slot_wal_keep_size") {
		t.Errorf("level %s, reasons %q, want danger because the limit is minutes away", st.Level, st.Reasons)
	}
}

func TestRateLooksAtRecentHistory(t *testing.T) {
	h := []sample{
		{t0, 0},
		{t0.Add(10 * time.Minute), 100 << 20}, // old growth, outside the window
		{t0.Add(12 * time.Minute), 100 << 20},
		{t0.Add(14 * time.Minute), 100 << 20},
	}
	if r := rate(h); r != 0 {
		t.Errorf("rate = %f, want 0 because nothing grew in the last few minutes", r)
	}
}

func TestDroppedSlotsAreForgotten(t *testing.T) {
	tr := NewTracker(DefaultLimits)
	tr.Update(snap(t0, -1, pg.Slot{Name: "a"}, pg.Slot{Name: "b"}))
	tr.Update(snap(t0.Add(time.Second), -1, pg.Slot{Name: "b"}))
	if _, ok := tr.history["a"]; ok {
		t.Error("history of a dropped slot is still kept")
	}
	if _, ok := tr.idleSince["a"]; ok {
		t.Error("idle time of a dropped slot is still kept")
	}
}

func TestWorstFirst(t *testing.T) {
	sts := NewTracker(DefaultLimits).Update(snap(t0, -1,
		pg.Slot{Name: "fine", Active: true, Retained: 5 << 20},
		pg.Slot{Name: "big", Active: true, Retained: 3 << 30},
		pg.Slot{Name: "lost", WALStatus: "lost"},
		pg.Slot{Name: "bigger", Active: true, Retained: 5 << 30},
	))
	var names []string
	for _, st := range sts {
		names = append(names, st.Name)
	}
	if got := strings.Join(names, " "); got != "lost bigger big fine" {
		t.Errorf("order = %s, want lost bigger big fine", got)
	}
}

func TestBytes(t *testing.T) {
	tests := map[int64]string{
		-1:         "-",
		0:          "0 bytes",
		1023:       "1023 bytes",
		1536:       "1.5 kB",
		16 << 20:   "16.0 MB",
		210 << 20:  "210 MB",
		6700 << 20: "6.5 GB",
		3 << 40:    "3.0 TB",
	}
	for n, want := range tests {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestParseBytes(t *testing.T) {
	tests := map[string]int64{
		"1GB":    1 << 30,
		"10gb":   10 << 30,
		"512MB":  512 << 20,
		"1.5 GB": 3 << 29,
		"64kb":   64 << 10,
		"1024":   1024,
		"2TB":    2 << 40,
	}
	for s, want := range tests {
		got, err := ParseBytes(s)
		if err != nil || got != want {
			t.Errorf("ParseBytes(%q) = %d, %v, want %d", s, got, err, want)
		}
	}
	for _, bad := range []string{"", "lots", "-1GB", "GB"} {
		if _, err := ParseBytes(bad); err == nil {
			t.Errorf("ParseBytes(%q) should fail", bad)
		}
	}
}

func TestDuration(t *testing.T) {
	tests := map[time.Duration]string{
		42 * time.Second:             "42s",
		3 * time.Minute:              "3m",
		2*time.Hour + 41*time.Minute: "2h 41m",
		9*24*time.Hour + 5*time.Hour: "9d 5h",
	}
	for d, want := range tests {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestRate(t *testing.T) {
	if got := Rate(0); got != "steady" {
		t.Errorf("Rate(0) = %q, want steady", got)
	}
	if got := Rate(1 << 20); got != "60.0 MB/min" {
		t.Errorf("Rate(1MB/s) = %q, want 60.0 MB/min", got)
	}
}
