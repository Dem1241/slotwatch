package ui

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Dem1241/slotwatch/internal/demo"
	"github.com/Dem1241/slotwatch/internal/slots"
)

// TestScreenshot writes a frame of the demo, a few minutes in, as ANSI to the
// file in SLOTWATCH_SCREENSHOT. It's how assets/slots.png is made:
//
//	SLOTWATCH_SCREENSHOT=frame.ansi go test ./internal/ui -run TestScreenshot
//	freeze frame.ansi -o assets/slots.png
func TestScreenshot(t *testing.T) {
	out := os.Getenv("SLOTWATCH_SCREENSHOT")
	if out == "" {
		t.Skip("set SLOTWATCH_SCREENSHOT to write a frame of the demo")
	}
	start := time.Date(2026, 10, 24, 14, 20, 0, 0, time.Local)
	srv := demo.New(start)
	var m tea.Model = New(srv, slots.DefaultLimits, 2*time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 132, Height: 32})
	for i := range 90 {
		m, _ = m.Update(snapshotMsg{snap: srv.At(start.Add(time.Duration(i) * 2 * time.Second))})
	}
	m = press(t, m.(Model), "down")
	if err := os.WriteFile(out, []byte(m.(Model).View().Content), 0o644); err != nil {
		t.Fatal(err)
	}
}
