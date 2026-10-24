package demo

import (
	"testing"
	"time"
)

func TestTheStuckSlotKeepsGrowing(t *testing.T) {
	start := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	s := New(start)
	first := s.At(start)
	later := s.At(start.Add(time.Minute))

	var before, after int64
	for i, sl := range first.Slots {
		if sl.Name == "debezium_shop" {
			before, after = sl.Retained, later.Slots[i].Retained
		}
	}
	if after-before < 30<<20 {
		t.Errorf("debezium_shop grew %d bytes in a minute, want a steady pile up", after-before)
	}
	if later.Server.MaxSlotWALKeep <= 0 || later.Server.Version < 170000 {
		t.Errorf("server = %+v", later.Server)
	}
}

func TestSnapshotsAreCopies(t *testing.T) {
	start := time.Now()
	s := New(start)
	snap := s.At(start)
	snap.Slots[0].Name = "changed"
	if s.At(start).Slots[0].Name == "changed" {
		t.Error("changing a snapshot changed the server")
	}
}
