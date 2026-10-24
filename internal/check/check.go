// Package check is slotwatch without the interface: one look at the slots,
// a report, and an exit code that cron, CI or a Kubernetes probe can act on.
package check

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/Dem1241/slotwatch/internal/pg"
	"github.com/Dem1241/slotwatch/internal/slots"
)

// Exit codes, in the style of monitoring plugins.
const (
	ExitOK      = 0
	ExitWarn    = 1
	ExitDanger  = 2
	ExitUnknown = 3
)

// Code is the exit code for a set of statuses: the worst level wins.
func Code(sts []slots.Status) int {
	worst := slots.OK
	for _, st := range sts {
		worst = max(worst, st.Level)
	}
	switch worst {
	case slots.Danger:
		return ExitDanger
	case slots.Warn:
		return ExitWarn
	}
	return ExitOK
}

// Text writes a table of the slots, with the reasons under any that aren't ok.
func Text(w io.Writer, snap pg.Snapshot, sts []slots.Status) error {
	if len(sts) == 0 {
		_, err := fmt.Fprintf(w, "no replication slots on %s\n", snap.Server.Name)
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "LEVEL\tSLOT\tTYPE\tSTATE\tRETAINED\tWAL STATUS")
	for _, st := range sts {
		state := "idle"
		switch {
		case st.Invalidated != "" || st.WALStatus == "lost":
			state = "lost"
		case st.Active:
			state = "active"
		case st.InactiveFor > 0:
			state = "idle " + slots.Duration(st.InactiveFor)
		}
		kind := st.Type
		if st.Plugin != "" {
			kind += "/" + st.Plugin
		}
		retained := slots.Bytes(st.Retained)
		if state == "lost" {
			retained = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", st.Level, st.Name, kind, state, retained, orDash(st.WALStatus))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, st := range sts {
		if st.Level == slots.OK {
			continue
		}
		fmt.Fprintf(w, "\n%s %s\n", st.Level, st.Name)
		for _, r := range st.Reasons {
			fmt.Fprintf(w, "  - %s\n", r)
		}
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// slotJSON is how a slot looks in --json output.
type slotJSON struct {
	Name          string     `json:"name"`
	Level         string     `json:"level"`
	Reasons       []string   `json:"reasons"`
	Type          string     `json:"type"`
	Plugin        string     `json:"plugin,omitempty"`
	Database      string     `json:"database,omitempty"`
	Active        bool       `json:"active"`
	Client        string     `json:"client,omitempty"`
	RetainedBytes int64      `json:"retained_bytes"`
	LagBytes      *int64     `json:"lag_bytes,omitempty"`
	WALStatus     string     `json:"wal_status,omitempty"`
	Invalidated   string     `json:"invalidated,omitempty"`
	InactiveSince *time.Time `json:"inactive_since,omitempty"`
}

// JSON writes the slots and the server they were read from.
func JSON(w io.Writer, snap pg.Snapshot, sts []slots.Status) error {
	out := struct {
		Server         string     `json:"server"`
		Version        int        `json:"server_version_num"`
		MaxSlotWALKeep int64      `json:"max_slot_wal_keep_size_bytes"`
		CheckedAt      time.Time  `json:"checked_at"`
		Level          string     `json:"level"`
		Slots          []slotJSON `json:"slots"`
	}{
		Server:         snap.Server.Name,
		Version:        snap.Server.Version,
		MaxSlotWALKeep: snap.Server.MaxSlotWALKeep,
		CheckedAt:      snap.At,
		Level:          levelOf(Code(sts)),
		Slots:          []slotJSON{},
	}
	for _, st := range sts {
		s := slotJSON{
			Name: st.Name, Level: st.Level.String(), Reasons: st.Reasons, Type: st.Type, Plugin: st.Plugin,
			Database: st.Database, Active: st.Active, Client: st.Client, RetainedBytes: st.Retained,
			WALStatus: st.WALStatus, Invalidated: st.Invalidated,
		}
		if s.Reasons == nil {
			s.Reasons = []string{}
		}
		if st.Lag >= 0 {
			lag := st.Lag
			s.LagBytes = &lag
		}
		if !st.Active && st.InactiveFor > 0 {
			since := snap.At.Add(-st.InactiveFor)
			s.InactiveSince = &since
		}
		out.Slots = append(out.Slots, s)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func levelOf(code int) string {
	return []string{"ok", "warn", "danger", "unknown"}[code]
}
