// Package slots decides how worried to be about each replication slot, and
// remembers enough history to say how fast it is growing.
package slots

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Dem1241/slotwatch/internal/pg"
)

// Level is how bad a slot looks.
type Level int

const (
	OK Level = iota
	Warn
	Danger
)

func (l Level) String() string {
	switch l {
	case Warn:
		return "warn"
	case Danger:
		return "danger"
	}
	return "ok"
}

// Limits are the thresholds a slot is judged by.
type Limits struct {
	Warn     int64         // retained WAL worth a warning
	Danger   int64         // retained WAL worth paging someone
	Inactive time.Duration // how long a slot may sit unused before it's a warning
}

// DefaultLimits are a sensible start for most databases.
var DefaultLimits = Limits{Warn: 1 << 30, Danger: 10 << 30, Inactive: 10 * time.Minute}

// Status is a slot with everything slotwatch worked out about it.
type Status struct {
	pg.Slot
	Level   Level
	Reasons []string // why it isn't ok, most important first

	// InactiveFor is how long the slot has been unused, from the server when
	// it knows (PostgreSQL 17+), otherwise since slotwatch first saw it idle.
	InactiveFor time.Duration
	// Rate is how fast Retained grows in bytes per second, from recent history.
	Rate float64
	// UntilLimit is when the slot reaches max_slot_wal_keep_size at the
	// current rate, or zero when it isn't growing or there is no limit.
	UntilLimit time.Duration
	// History is the retained WAL of recent snapshots, oldest first.
	History []int64
}

// sample is one point of a slot's history.
type sample struct {
	at       time.Time
	retained int64
}

// historyLen is how many snapshots a slot remembers, enough for a sparkline
// and a rate over the last few minutes.
const historyLen = 120

// rateWindow is how far back the growth rate looks.
const rateWindow = 5 * time.Minute

// Tracker turns a stream of snapshots into statuses.
type Tracker struct {
	limits    Limits
	history   map[string][]sample
	idleSince map[string]time.Time
}

// NewTracker starts with no history.
func NewTracker(limits Limits) *Tracker {
	return &Tracker{limits: limits, history: map[string][]sample{}, idleSince: map[string]time.Time{}}
}

// Update adds a snapshot and returns the status of every slot in it, the
// worst first.
func (t *Tracker) Update(snap pg.Snapshot) []Status {
	seen := map[string]bool{}
	out := make([]Status, 0, len(snap.Slots))
	for _, s := range snap.Slots {
		seen[s.Name] = true
		h := append(t.history[s.Name], sample{snap.At, s.Retained})
		if len(h) > historyLen {
			h = h[len(h)-historyLen:]
		}
		t.history[s.Name] = h

		if s.Active {
			delete(t.idleSince, s.Name)
		} else if _, ok := t.idleSince[s.Name]; !ok {
			t.idleSince[s.Name] = snap.At
		}

		st := Status{Slot: s, Rate: rate(h)}
		for _, p := range h {
			st.History = append(st.History, p.retained)
		}
		if !s.Active {
			since := t.idleSince[s.Name]
			if !s.InactiveSince.IsZero() {
				since = s.InactiveSince
			}
			st.InactiveFor = snap.At.Sub(since)
		}
		if limit := snap.Server.MaxSlotWALKeep; limit > 0 && st.Rate > 0 && s.Retained < limit {
			st.UntilLimit = time.Duration(float64(limit-s.Retained) / st.Rate * float64(time.Second))
		}
		st.Level, st.Reasons = t.judge(st, snap.Server)
		out = append(out, st)
	}
	// forget slots that were dropped
	for name := range t.history {
		if !seen[name] {
			delete(t.history, name)
			delete(t.idleSince, name)
		}
	}
	slices.SortStableFunc(out, func(a, b Status) int {
		return cmp.Or(cmp.Compare(b.Level, a.Level), cmp.Compare(b.Retained, a.Retained))
	})
	return out
}

// rate is the growth of retained WAL in bytes per second over rateWindow.
func rate(h []sample) float64 {
	if len(h) < 2 {
		return 0
	}
	last := h[len(h)-1]
	first := h[0]
	for _, p := range h {
		if last.at.Sub(p.at) <= rateWindow {
			first = p
			break
		}
	}
	secs := last.at.Sub(first.at).Seconds()
	if secs <= 0 {
		return 0
	}
	return float64(last.retained-first.retained) / secs
}

func (t *Tracker) judge(st Status, srv pg.Server) (Level, []string) {
	level := OK
	var reasons []string
	raise := func(l Level, reason string) {
		level = max(level, l)
		reasons = append(reasons, reason)
	}

	switch {
	case st.Invalidated != "":
		raise(Danger, "invalidated: "+st.Invalidated)
	case st.WALStatus == "lost":
		raise(Danger, "the WAL this slot needs is gone")
	case st.WALStatus == "unreserved":
		raise(Danger, "past max_slot_wal_keep_size, WAL will be removed at the next checkpoint")
	}

	switch {
	case st.Retained >= t.limits.Danger:
		raise(Danger, "holding "+Bytes(st.Retained)+" of WAL")
	case st.Retained >= t.limits.Warn:
		raise(Warn, "holding "+Bytes(st.Retained)+" of WAL")
	}

	if !st.Active && st.InactiveFor >= t.limits.Inactive {
		raise(Warn, "unused for "+Duration(st.InactiveFor))
	}
	if st.WALStatus == "extended" {
		raise(Warn, "past max_wal_size, the WAL is still kept for this slot")
	}
	if st.Rate > 0 && st.UntilLimit > 0 && st.UntilLimit < time.Hour {
		raise(Danger, "reaches max_slot_wal_keep_size in about "+Duration(st.UntilLimit))
	}
	if level > OK && srv.MaxSlotWALKeep < 0 && st.Invalidated == "" {
		reasons = append(reasons, "max_slot_wal_keep_size is unlimited, so this grows until the disk is full")
	}
	return level, reasons
}

// Bytes formats a size the way PostgreSQL's pg_size_pretty does.
func Bytes(n int64) string {
	if n < 0 {
		return "-"
	}
	units := []string{"bytes", "kB", "MB", "GB", "TB", "PB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d bytes", n)
	}
	if f >= 100 {
		return fmt.Sprintf("%.0f %s", f, units[i])
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// Duration formats a duration as the two largest units, like 3h 12m.
func Duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

// Rate formats bytes per second as per minute, which reads better for WAL.
func Rate(bytesPerSec float64) string {
	if bytesPerSec <= 0 {
		return "steady"
	}
	return Bytes(int64(bytesPerSec*60)) + "/min"
}

// ParseBytes reads a size like 512MB, 10GB or 1073741824. Units are powers
// of 1024, as in PostgreSQL.
func ParseBytes(s string) (int64, error) {
	units := []struct {
		suffix string
		mult   int64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}}
	clean := strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range units {
		if n, ok := strings.CutSuffix(clean, u.suffix); ok {
			clean, mult = strings.TrimSpace(n), u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(clean, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("can't read %q as a size, try something like 10GB", s)
	}
	return int64(f * float64(mult)), nil
}
