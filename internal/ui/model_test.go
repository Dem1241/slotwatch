package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Dem1241/slotwatch/internal/pg"
	"github.com/Dem1241/slotwatch/internal/slots"
)

type fakeSource struct {
	snap pg.Snapshot
	err  error
}

func (f *fakeSource) Snapshot(context.Context) (pg.Snapshot, error) { return f.snap, f.err }

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func shop() pg.Snapshot {
	return pg.Snapshot{
		At:     t0,
		Server: pg.Server{Name: "db/shop", Version: 170002, MaxSlotWALKeep: 20 << 30, MaxSlots: 10},
		Slots: []pg.Slot{
			{Name: "airbyte_orders", Type: "logical", Plugin: "pgoutput", Database: "shop", Active: true, ActivePID: 4821, Client: "airbyte", Retained: 38 << 20, WALStatus: "reserved"},
			{Name: "debezium_shop", Type: "logical", Plugin: "pgoutput", Database: "shop", Retained: 6 << 30, Lag: 6 << 30, WALStatus: "extended", InactiveSince: t0.Add(-3 * time.Hour)},
			{Name: "replica_eu", Type: "physical", Active: true, Client: "pg-replica-eu", Retained: 16 << 20, Lag: -1, WALStatus: "reserved"},
			{Name: "old_test", Type: "logical", Plugin: "wal2json", Lag: -1, WALStatus: "lost", Invalidated: "wal_removed"},
		},
	}
}

// load builds a model that has seen one snapshot of src, at the given size.
func load(t *testing.T, src *fakeSource, w, h int) Model {
	t.Helper()
	var m tea.Model = New(src, slots.DefaultLimits, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m, _ = m.Update(m.(Model).Init()())
	return m.(Model)
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	var tm tea.Model = m
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "up":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "space":
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		default:
			msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
		}
		tm, _ = tm.Update(msg)
	}
	return tm.(Model)
}

func screen(m Model) string { return ansi.Strip(m.View().Content) }

func TestShowsTheWorstSlotFirst(t *testing.T) {
	m := load(t, &fakeSource{snap: shop()}, 140, 40)
	s := screen(m)
	for _, want := range []string{"≋ slotwatch", "db/shop", "postgres 17", "● live", "6.1 GB of WAL held by 4 slots", "1 in danger", "1 to watch"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen is missing %q:\n%s", want, s)
		}
	}
	lost, deb, air := strings.Index(s, "old_test"), strings.Index(s, "debezium_shop"), strings.Index(s, "airbyte_orders")
	if lost < 0 || deb < lost || air < deb {
		t.Errorf("want old_test, then debezium_shop, then the healthy ones:\n%s", s)
	}
}

func TestDetailExplainsAndSuggests(t *testing.T) {
	m := press(t, load(t, &fakeSource{snap: shop()}, 140, 40), "down")
	s := screen(m)
	for _, want := range []string{
		"unused for", "3h 0m",
		"not confirmed", "6.0 GB",
		"extended", "past max_wal_size",
		"why it's flagged", "holding 6.0 GB of WAL",
		"what you can do", "SELECT pg_drop_replication_slot('debezium_shop');",
		"until max_slot_wal_keep_size",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("detail is missing %q:\n%s", want, s)
		}
	}
}

func TestHealthySlotHasNoAdvice(t *testing.T) {
	m := press(t, load(t, &fakeSource{snap: shop()}, 140, 40), "down", "down")
	s := screen(m)
	if strings.Contains(s, "what you can do") {
		t.Errorf("a healthy slot got advice:\n%s", s)
	}
	if !strings.Contains(s, "airbyte") || !strings.Contains(s, "pid 4821") {
		t.Errorf("detail doesn't say who uses the slot:\n%s", s)
	}
}

func TestUnlimitedKeepSizeSuggestsALimit(t *testing.T) {
	snap := shop()
	snap.Server.MaxSlotWALKeep = -1
	m := press(t, load(t, &fakeSource{snap: snap}, 140, 50), "down")
	if s := screen(m); !strings.Contains(s, "ALTER SYSTEM SET max_slot_wal_keep_size") {
		t.Errorf("no suggestion to set max_slot_wal_keep_size:\n%s", s)
	}
}

func TestFilter(t *testing.T) {
	m := press(t, load(t, &fakeSource{snap: shop()}, 140, 40), "/", "a", "i", "r", "enter")
	s := screen(m)
	if !strings.Contains(s, "airbyte_orders") || strings.Contains(s, "debezium_shop") {
		t.Errorf("filter for air should show only airbyte_orders:\n%s", s)
	}
	m = press(t, m, "esc")
	if s := screen(m); !strings.Contains(s, "debezium_shop") {
		t.Errorf("esc should clear the filter:\n%s", s)
	}
}

func TestSortKeepsTheSelection(t *testing.T) {
	m := press(t, load(t, &fakeSource{snap: shop()}, 140, 40), "down") // debezium_shop
	m = press(t, m, "s", "s")                                          // by name
	if got := m.selectedName(); got != "debezium_shop" {
		t.Errorf("selected %q after sorting, want debezium_shop", got)
	}
	var names []string
	for _, st := range m.visible() {
		names = append(names, st.Name)
	}
	if got := strings.Join(names, " "); got != "airbyte_orders debezium_shop old_test replica_eu" {
		t.Errorf("sorted by name = %s", got)
	}
}

func TestPauseStopsRefreshing(t *testing.T) {
	m := press(t, load(t, &fakeSource{snap: shop()}, 140, 40), "space")
	if !strings.Contains(screen(m), "paused") {
		t.Error("header doesn't say paused")
	}
	tm, _ := m.Update(tickMsg{})
	if tm.(Model).loading {
		t.Error("a tick while paused started a refresh")
	}
}

func TestServerError(t *testing.T) {
	src := &fakeSource{snap: shop()}
	m := load(t, src, 140, 40)
	src.err = errors.New("connection refused")
	tm, _ := m.Update(m.Init()())
	s := screen(tm.(Model))
	if !strings.Contains(s, "can't reach the server") || !strings.Contains(s, "connection refused") {
		t.Errorf("error isn't shown:\n%s", s)
	}
	if !strings.Contains(s, "debezium_shop") {
		t.Errorf("the last good snapshot should stay on screen:\n%s", s)
	}
}

func TestNoSlots(t *testing.T) {
	snap := shop()
	snap.Slots = nil
	s := screen(load(t, &fakeSource{snap: snap}, 140, 40))
	if !strings.Contains(s, "no replication slots") {
		t.Errorf("empty server isn't explained:\n%s", s)
	}
}

func TestNarrowTerminalStacksThePanes(t *testing.T) {
	m := load(t, &fakeSource{snap: shop()}, 80, 40)
	lines := strings.Split(screen(m), "\n")
	if len(lines) != 40 {
		t.Errorf("screen has %d lines, want 40", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 80 {
			t.Errorf("line %d is %d wide, more than the terminal: %q", i, w, l)
		}
	}
}

func TestSparkline(t *testing.T) {
	if got := sparkline([]int64{0, 1 << 25, 2 << 25, 3 << 25, 4 << 25, 5 << 25, 6 << 25, 7 << 25}, 8); got != "▁▂▃▄▅▆▇█" {
		t.Errorf("sparkline = %q", got)
	}
	if got := sparkline([]int64{5, 5}, 4); got != "  ▁▁" {
		t.Errorf("flat sparkline = %q", got)
	}
	if got := sparkline(nil, 3); got != "   " {
		t.Errorf("empty sparkline = %q", got)
	}
}

func TestSparklineIgnoresSmallWobbles(t *testing.T) {
	if got := sparkline([]int64{16 << 20, 17 << 20, 16 << 20, 18 << 20}, 4); got != "▁▁▁▁" {
		t.Errorf("a slot wobbling by 2 MB drew %q, want it flat", got)
	}
}
