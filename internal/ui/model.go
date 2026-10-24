// Package ui is slotwatch's terminal interface: a list of replication slots,
// worst first, and the details of the one you pick.
package ui

import (
	"context"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Dem1241/slotwatch/internal/pg"
	"github.com/Dem1241/slotwatch/internal/slots"
)

// Source is where snapshots come from; *pg.Client is one.
type Source interface {
	Snapshot(ctx context.Context) (pg.Snapshot, error)
}

// sortOrder is how the slot list is sorted.
type sortOrder int

const (
	byHealth sortOrder = iota
	byRetained
	byName
)

func (o sortOrder) String() string {
	switch o {
	case byRetained:
		return "retained"
	case byName:
		return "name"
	}
	return "health"
}

type Model struct {
	src      Source
	tracker  *slots.Tracker
	interval time.Duration

	width, height int

	server   pg.Server
	statuses []slots.Status
	updated  time.Time // when the last snapshot was taken
	err      error     // from the last snapshot, cleared by the next good one
	loading  bool

	idx       int // index into the visible (filtered) slots
	detailTop int // scroll position of the detail pane
	paused    bool
	sort      sortOrder

	filtering bool
	filter    string
}

// New creates the interface, refreshing every interval.
func New(src Source, limits slots.Limits, interval time.Duration) Model {
	return Model{src: src, tracker: slots.NewTracker(limits), interval: interval, loading: true}
}

type (
	snapshotMsg struct {
		snap pg.Snapshot
		err  error
	}
	tickMsg struct{}
)

func (m Model) Init() tea.Cmd { return fetch(m.src) }

func fetch(src Source) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		snap, err := src.Snapshot(ctx)
		return snapshotMsg{snap, err}
	}
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case snapshotMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, m.tick()
		}
		selected := m.selectedName()
		m.err = nil
		m.server = msg.snap.Server
		m.statuses = m.tracker.Update(msg.snap)
		m.updated = msg.snap.At
		m.sortStatuses()
		m.keepSelection(selected)
		return m, m.tick()

	case tickMsg:
		if m.paused {
			return m, m.tick()
		}
		m.loading = true
		return m, fetch(m.src)

	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	if m.filtering {
		switch s {
		case "esc":
			m.filtering, m.filter = false, ""
		case "enter":
			m.filtering = false
		case "backspace":
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
		default:
			if k.Text != "" {
				m.filter += k.Text
			}
		}
		m.idx, m.detailTop = 0, 0
		return m, nil
	}

	n := len(m.visible())
	switch s {
	case "q":
		return m, tea.Quit
	case "/":
		m.filtering = true
	case "esc":
		m.filter = ""
	case "up", "k":
		m.move(-1, n)
	case "down", "j":
		m.move(1, n)
	case "home", "g":
		m.move(-n, n)
	case "end", "G":
		m.move(n, n)
	case "ctrl+d", "J":
		m.detailTop += 5
	case "ctrl+u", "K":
		m.detailTop = max(0, m.detailTop-5)
	case "space":
		m.paused = !m.paused
	case "s":
		selected := m.selectedName()
		m.sort = (m.sort + 1) % 3
		m.sortStatuses()
		m.keepSelection(selected)
	case "r":
		if !m.loading {
			m.loading = true
			return m, fetch(m.src)
		}
	}
	return m, nil
}

func (m *Model) move(by, n int) {
	m.idx = clamp(m.idx+by, 0, n-1)
	m.detailTop = 0
}

// visible is the slots that match the filter.
func (m Model) visible() []slots.Status {
	if m.filter == "" {
		return m.statuses
	}
	f := strings.ToLower(m.filter)
	var out []slots.Status
	for _, st := range m.statuses {
		hay := strings.ToLower(st.Name + " " + st.Plugin + " " + st.Database + " " + st.Client + " " + st.Level.String())
		if strings.Contains(hay, f) {
			out = append(out, st)
		}
	}
	return out
}

func (m Model) selectedName() string {
	if v := m.visible(); m.idx >= 0 && m.idx < len(v) {
		return v[m.idx].Name
	}
	return ""
}

// keepSelection keeps the same slot selected when the list changes under it.
func (m *Model) keepSelection(name string) {
	v := m.visible()
	if i := slices.IndexFunc(v, func(st slots.Status) bool { return st.Name == name }); i >= 0 {
		m.idx = i
	}
	m.idx = clamp(m.idx, 0, len(v)-1)
}

func (m *Model) sortStatuses() {
	switch m.sort {
	case byRetained:
		slices.SortStableFunc(m.statuses, func(a, b slots.Status) int { return compareInt64(b.Retained, a.Retained) })
	case byName:
		slices.SortStableFunc(m.statuses, func(a, b slots.Status) int { return strings.Compare(a.Name, b.Name) })
	default:
		slices.SortStableFunc(m.statuses, func(a, b slots.Status) int {
			if a.Level != b.Level {
				return int(b.Level) - int(a.Level)
			}
			return compareInt64(b.Retained, a.Retained)
		})
	}
}

func compareInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (m Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.WindowTitle = "slotwatch"
	if m.width == 0 {
		return v
	}
	v.Content = lipgloss.JoinVertical(lipgloss.Left, m.header(), m.body(), m.footer())
	return v
}

func (m Model) bodyHeight() int { return max(3, m.height-2) }

func (m Model) footer() string {
	if m.filtering {
		return " " + sPink.Render("/") + sText.Render(m.filter) + sPink.Render("▏") + "  " + hints("enter", "keep", "esc", "clear")
	}
	pause := "pause"
	if m.paused {
		pause = "resume"
	}
	h := hints("↑↓", "move", "s", "sort by "+((m.sort+1)%3).String(), "space", pause, "r", "refresh", "ctrl+d/u", "scroll detail", "/", "filter", "q", "quit")
	if m.filter != "" {
		h = sPink.Render("/") + sText.Render(m.filter) + "  " + h
	}
	if m.err != nil {
		h = sErr.Render(oneLine(m.err.Error())) + "  " + h
	}
	return ansi.Truncate(" "+h, m.width, "…")
}

func hints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sKey.Render(pairs[i])+" "+sDim.Render(pairs[i+1]))
	}
	return strings.Join(parts, "   ")
}

// spread puts left and right on one line of the given width.
func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
