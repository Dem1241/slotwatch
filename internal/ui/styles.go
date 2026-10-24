package ui

import (
	"charm.land/lipgloss/v2"

	"github.com/Dem1241/slotwatch/internal/slots"
)

// Catppuccin Mocha. The terminal's own background shows through; only the
// badges get a background of their own.
var (
	pink     = lipgloss.Color("#f5c2e7")
	mauve    = lipgloss.Color("#cba6f7")
	text     = lipgloss.Color("#cdd6f4")
	subtext  = lipgloss.Color("#a6adc8")
	overlay  = lipgloss.Color("#6c7086")
	surface1 = lipgloss.Color("#45475a")
	base     = lipgloss.Color("#1e1e2e")
	green    = lipgloss.Color("#a6e3a1")
	yellow   = lipgloss.Color("#f9e2af")
	red      = lipgloss.Color("#f38ba8")
	blue     = lipgloss.Color("#89b4fa")
	peach    = lipgloss.Color("#fab387")
)

var (
	sText   = lipgloss.NewStyle().Foreground(text)
	sSub    = lipgloss.NewStyle().Foreground(subtext)
	sDim    = lipgloss.NewStyle().Foreground(overlay)
	sPink   = lipgloss.NewStyle().Foreground(pink)
	sTitle  = lipgloss.NewStyle().Foreground(pink).Bold(true)
	sErr    = lipgloss.NewStyle().Foreground(red)
	sKey    = lipgloss.NewStyle().Foreground(pink)
	sPane   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(surface1).Padding(0, 1)
	sPaneOn = sPane.BorderForeground(pink)
	sOK     = lipgloss.NewStyle().Foreground(green)
	sWarn   = lipgloss.NewStyle().Foreground(yellow)
	sDanger = lipgloss.NewStyle().Foreground(red)
	sSQL    = lipgloss.NewStyle().Foreground(blue)
	sSize   = lipgloss.NewStyle().Foreground(peach)
	sLabel  = lipgloss.NewStyle().Foreground(mauve)
)

// levelStyle is the colour of a health level.
func levelStyle(l slots.Level) lipgloss.Style {
	switch l {
	case slots.Danger:
		return sDanger
	case slots.Warn:
		return sWarn
	}
	return sOK
}

// badge is the coloured pill in front of every slot: what state it is in,
// in the colour of how worried to be about it.
func badge(st slots.Status) string {
	label := "idle"
	switch {
	case st.Invalidated != "" || st.WALStatus == "lost":
		label = "lost"
	case st.Active:
		label = "active"
	}
	bg := green
	switch st.Level {
	case slots.Danger:
		bg = red
	case slots.Warn:
		bg = yellow
	}
	return lipgloss.NewStyle().Foreground(base).Background(bg).Width(8).Align(lipgloss.Center).Render(label)
}
