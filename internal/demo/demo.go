// Package demo is a pretend PostgreSQL server with a few replication slots,
// one of them a Debezium connector that stopped and keeps piling up WAL, so
// slotwatch can be tried without a database.
package demo

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Dem1241/slotwatch/internal/pg"
)

// Server hands out snapshots that move a little every time.
type Server struct {
	mu    sync.Mutex
	start time.Time
	last  time.Time
	slots []pg.Slot
	rng   *rand.Rand
}

// New starts the pretend server at now.
func New(now time.Time) *Server {
	return &Server{
		start: now,
		last:  now,
		rng:   rand.New(rand.NewPCG(1, 2)),
		slots: []pg.Slot{
			{
				Name: "debezium_shop", Type: "logical", Plugin: "pgoutput", Database: "shop",
				Retained: 6_700 << 20, Lag: 6_650 << 20, WALStatus: "extended", SafeWALSize: 13_300 << 20,
				InactiveSince: now.Add(-2*time.Hour - 41*time.Minute),
			},
			{
				Name: "airbyte_orders", Type: "logical", Plugin: "pgoutput", Database: "shop",
				Active: true, ActivePID: 48211, Client: "airbyte", Retained: 38 << 20, Lag: 2 << 20,
				WALStatus: "reserved", SafeWALSize: 19_960 << 20,
			},
			{
				Name: "replica_eu_west", Type: "physical", Active: true, ActivePID: 48102,
				Client: "pg-replica-eu-west", Retained: 16 << 20, Lag: -1, WALStatus: "reserved", SafeWALSize: 19_990 << 20,
			},
			{
				Name: "analytics_test", Type: "logical", Plugin: "wal2json", Database: "analytics",
				Lag: -1, WALStatus: "lost", SafeWALSize: -1, Invalidated: "wal_removed",
				InactiveSince: now.Add(-9*24*time.Hour - 5*time.Hour),
			},
			{
				Name: "backup_standby", Type: "physical", Retained: 210 << 20, Lag: -1,
				WALStatus: "reserved", SafeWALSize: 19_800 << 20, InactiveSince: now.Add(-3 * time.Minute),
			},
		},
	}
}

// limit is the pretend max_slot_wal_keep_size.
const limit = 20 << 30

// Snapshot moves the slots along by the time that passed since the last call
// and returns them.
func (s *Server) Snapshot(context.Context) (pg.Snapshot, error) {
	return s.At(time.Now()), nil
}

// At is the snapshot at a given time, so tests and screenshots can play the
// demo faster than real time. Times must not go backwards.
func (s *Server) At(now time.Time) pg.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	secs := now.Sub(s.last).Seconds()
	s.last = now
	for i := range s.slots {
		sl := &s.slots[i]
		switch sl.Name {
		case "debezium_shop":
			// nobody reads it, so everything written since stays around
			grow := int64(secs * (650 + s.rng.Float64()*150) * 1024)
			sl.Retained += grow
			sl.Lag += grow
			sl.SafeWALSize = max(0, limit-sl.Retained)
		case "airbyte_orders", "replica_eu_west":
			// consumers keep up, so it only wobbles
			sl.Retained = max(4<<20, sl.Retained+int64((s.rng.Float64()-0.5)*secs*400*1024))
			if sl.Lag >= 0 {
				sl.Lag = min(sl.Retained, int64(s.rng.Float64()*3*(1<<20)))
			}
		}
	}
	return pg.Snapshot{
		At: now,
		Server: pg.Server{
			Version: 170002, WALLevel: "logical", MaxSlotWALKeep: limit, MaxSlots: 10,
			Name: "demo/shop",
		},
		Slots: append([]pg.Slot(nil), s.slots...),
	}
}
