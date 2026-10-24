package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Dem1241/slotwatch/internal/slots"
)

func (m Model) header() string {
	left := " " + sTitle.Render("≋ slotwatch")
	if m.server.Name != "" {
		left += sDim.Render(" · ") + sSub.Render(m.server.Name) + sDim.Render(" · ") + sSub.Render(pgVersion(m.server.Version))
		if m.server.InRecovery {
			left += sDim.Render(" · ") + sSub.Render("standby")
		}
	}

	var right string
	switch {
	case m.paused:
		right = sDim.Render("❚❚ paused")
	case m.err != nil:
		right = sErr.Render("○ can't reach the server")
	default:
		right = sOK.Render("● live")
	}
	if !m.updated.IsZero() {
		right += sDim.Render("  " + m.updated.Format("15:04:05"))
	}
	return spread(left, right+" ", m.width)
}

// pgVersion turns server_version_num into "postgres 17".
func pgVersion(n int) string {
	if n == 0 {
		return "postgres"
	}
	return fmt.Sprintf("postgres %d", n/10000)
}

// sideBySide is whether the list and the detail pane fit next to each other.
func (m Model) sideBySide() bool { return m.width >= 110 }

func (m Model) body() string {
	h := m.bodyHeight()
	listW, listH, detW, detH := m.width, h*45/100, m.width, h-h*45/100
	if m.sideBySide() {
		listW = m.width * 52 / 100
		listH, detW, detH = h, m.width-listW, h
	}

	sts := m.visible()
	rows := listH - 4
	inner := listW - 4
	var lines []string
	lines = append(lines, m.summary(inner), "")
	switch {
	case m.loading && len(m.statuses) == 0 && m.err == nil:
		lines = append(lines, sDim.Render("looking at the slots…"))
	case m.err != nil && len(m.statuses) == 0:
		lines = append(lines, sErr.Render("couldn't read the replication slots"), sSub.Render(oneLine(m.err.Error())))
	case len(m.statuses) == 0:
		lines = append(lines, sOK.Render("no replication slots"), sDim.Render("nothing can hold back WAL here, which is a good thing"))
	case len(sts) == 0:
		lines = append(lines, sDim.Render("nothing matches"))
	}
	start := clamp(m.idx-rows/2, 0, max(0, len(sts)-rows))
	for i := start; i < len(sts) && i < start+rows; i++ {
		lines = append(lines, row(sts[i], i == m.idx, inner))
	}
	list := sPaneOn.Width(listW).Height(listH).Render(strings.Join(fit(lines, listH-2), "\n"))

	var detail []string
	if m.idx >= 0 && m.idx < len(sts) {
		detail = detailLines(sts[m.idx], m.server.MaxSlotWALKeep, detW-4)
	}
	top := clamp(m.detailTop, 0, max(0, len(detail)-(detH-2)))
	det := sPane.Width(detW).Height(detH).Render(strings.Join(fit(detail[min(top, len(detail)):], detH-2), "\n"))

	if m.sideBySide() {
		return lipgloss.JoinHorizontal(lipgloss.Top, list, det)
	}
	return lipgloss.JoinVertical(lipgloss.Left, list, det)
}

// summary is the line above the slots: how much WAL they hold in total and
// how many need a look.
func (m Model) summary(width int) string {
	if len(m.statuses) == 0 {
		return sDim.Render(fmt.Sprintf("%d of %d slots used", 0, m.server.MaxSlots))
	}
	var total int64
	warn, danger := 0, 0
	for _, st := range m.statuses {
		total += max(0, st.Retained)
		switch st.Level {
		case slots.Warn:
			warn++
		case slots.Danger:
			danger++
		}
	}
	s := sSize.Render(slots.Bytes(total)) + sSub.Render(" of WAL held by ") + sText.Render(fmt.Sprintf("%d slot%s", len(m.statuses), plural(len(m.statuses))))
	switch {
	case danger > 0:
		s += sDim.Render(" · ") + sDanger.Render(fmt.Sprintf("%d in danger", danger))
		if warn > 0 {
			s += sDim.Render(", ") + sWarn.Render(fmt.Sprintf("%d to watch", warn))
		}
	case warn > 0:
		s += sDim.Render(" · ") + sWarn.Render(fmt.Sprintf("%d to watch", warn))
	default:
		s += sDim.Render(" · ") + sOK.Render("all good")
	}
	return ansi.Truncate(s, width, "…")
}

// row is one line of the slot list.
func row(st slots.Status, selected bool, width int) string {
	marker := "  "
	name := sText.Render(st.Name)
	if selected {
		marker = sPink.Render("▌ ")
		name = sTitle.Render(st.Name)
	}
	spark := sparkline(st.History, 10)
	right := lpad(retainedText(st), 10) + "  " + levelStyle(st.Level).Render(spark)
	kind := st.Plugin
	if kind == "" {
		kind = st.Type
	}
	left := marker + badge(st) + " " + name + sDim.Render(" "+kind)
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

func retainedText(st slots.Status) string {
	if st.Invalidated != "" || st.WALStatus == "lost" {
		return sDim.Render("—")
	}
	return sSize.Render(slots.Bytes(st.Retained))
}

// detailLines describes one slot: what it is, what it holds, why it was
// flagged and what can be done about it.
func detailLines(st slots.Status, limit int64, width int) []string {
	var lines []string
	add := func(s string) { lines = append(lines, ansi.Truncate(s, width, "…")) }
	field := func(label, value string) {
		add(sLabel.Render(pad(label, 14)) + value)
	}

	add(sTitle.Render(st.Name) + "  " + badge(st))
	add("")

	kind := st.Type
	if st.Plugin != "" {
		kind += sDim.Render(" · ") + st.Plugin
	}
	if st.Temporary {
		kind += sDim.Render(" · temporary")
	}
	field("type", sText.Render(kind))
	if st.Database != "" {
		field("database", sText.Render(st.Database))
	}
	switch {
	case st.Active:
		who := fmt.Sprintf("pid %d", st.ActivePID)
		if st.Client != "" {
			who = st.Client + sDim.Render(" · ") + who
		}
		field("used by", sOK.Render(who))
	case st.InactiveFor > 0:
		field("unused for", levelStyle(st.Level).Render(slots.Duration(st.InactiveFor)))
	default:
		field("used by", sDim.Render("nobody"))
	}
	add("")

	if st.Invalidated != "" || st.WALStatus == "lost" {
		field("retained", sDim.Render("— the WAL it needs was removed"))
	} else {
		field("retained", sSize.Render(slots.Bytes(st.Retained)))
	}
	if st.Lag >= 0 {
		field("not confirmed", sSize.Render(slots.Bytes(st.Lag)))
	}
	if st.WALStatus != "" {
		field("wal status", walStatusText(st.WALStatus))
	}
	field("growth", growthText(st))
	if limit > 0 && st.Invalidated == "" && st.WALStatus != "lost" {
		field("room left", sSub.Render(slots.Bytes(max(0, limit-st.Retained))+" until max_slot_wal_keep_size"))
		if st.UntilLimit > 0 {
			field("full in", levelStyle(st.Level).Render("about "+slots.Duration(st.UntilLimit)))
		}
	}
	if len(st.History) > 1 {
		add("")
		add(levelStyle(st.Level).Render(sparkline(st.History, max(10, width-2))))
		add(sDim.Render(fmt.Sprintf("retained WAL over the last %d updates", len(st.History))))
	}

	if len(st.Reasons) > 0 {
		add("")
		add(sTitle.Render("why it's flagged"))
		for _, r := range st.Reasons {
			add(levelStyle(st.Level).Render("• ") + sText.Render(r))
		}
	}
	if fixes := fixes(st, limit); len(fixes) > 0 {
		add("")
		add(sTitle.Render("what you can do"))
		for _, f := range fixes {
			for _, l := range wrap(f, width) {
				if strings.HasPrefix(l, "SELECT") || strings.HasPrefix(l, "ALTER") {
					add("  " + sSQL.Render(l))
				} else {
					add(sSub.Render(l))
				}
			}
		}
	}
	return lines
}

func walStatusText(s string) string {
	switch s {
	case "reserved":
		return sOK.Render("reserved") + sDim.Render(" · within max_wal_size")
	case "extended":
		return sWarn.Render("extended") + sDim.Render(" · past max_wal_size, still kept")
	case "unreserved":
		return sDanger.Render("unreserved") + sDim.Render(" · will be removed at the next checkpoint")
	case "lost":
		return sDanger.Render("lost") + sDim.Render(" · the slot can't be used anymore")
	}
	return sText.Render(s)
}

func growthText(st slots.Status) string {
	if len(st.History) < 2 {
		return sDim.Render("measuring…")
	}
	if st.Rate <= 0 {
		return sOK.Render("steady")
	}
	return levelStyle(st.Level).Render("+" + slots.Rate(st.Rate))
}

// fixes are the suggestions for a slot. slotwatch never runs them.
func fixes(st slots.Status, limit int64) []string {
	if st.Level == slots.OK {
		return nil
	}
	drop := fmt.Sprintf("SELECT pg_drop_replication_slot('%s');", strings.ReplaceAll(st.Name, "'", "''"))
	var out []string
	switch {
	case st.Invalidated != "" || st.WALStatus == "lost":
		out = append(out,
			"this slot can't be used anymore. drop it, and set the consumer up again with a fresh snapshot:",
			drop)
	case !st.Active && st.Type == "logical":
		out = append(out,
			"nothing is reading this slot. check the consumer it belongs to (a debezium connector, airbyte, a subscription), or if it isn't needed anymore:",
			drop)
	case !st.Active:
		out = append(out,
			"no standby is connected. check the replica that uses this slot, or if it's gone:",
			drop)
	default:
		out = append(out, "the consumer is connected but falling behind. check that it keeps up and confirms what it has read.")
	}
	if limit < 0 {
		out = append(out,
			"set a ceiling so a stuck slot can't fill the disk. the slot is invalidated instead once it holds more than this:",
			"ALTER SYSTEM SET max_slot_wal_keep_size = '50GB';", "SELECT pg_reload_conf();")
	}
	return out
}

// sparkLevels are the bars of a sparkline, lowest first.
var sparkLevels = []rune("▁▂▃▄▅▆▇█")

// sparkMinSpan is the smallest range a sparkline is scaled to, so a slot that
// only wobbles by a few megabytes stays flat instead of looking dramatic.
const sparkMinSpan = 32 << 20

// sparkline draws the last width values, scaled between their min and max.
func sparkline(values []int64, width int) string {
	if len(values) > width {
		values = values[len(values)-width:]
	}
	if len(values) == 0 {
		return strings.Repeat(" ", width)
	}
	lo, hi := values[0], values[0]
	for _, v := range values {
		lo, hi = min(lo, v), max(hi, v)
	}
	hi = max(hi, lo+sparkMinSpan)
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", width-len(values)))
	for _, v := range values {
		i := 0
		if hi > lo {
			i = int(float64(v-lo) / float64(hi-lo) * float64(len(sparkLevels)-1))
		}
		b.WriteRune(sparkLevels[i])
	}
	return b.String()
}

// wrap breaks text into lines of at most width, keeping SQL on one line.
func wrap(s string, width int) []string {
	if width <= 0 || lipgloss.Width(s) <= width || strings.HasPrefix(s, "SELECT") || strings.HasPrefix(s, "ALTER") {
		return []string{s}
	}
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		if line != "" && lipgloss.Width(line)+1+lipgloss.Width(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	return append(lines, line)
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}

func lpad(s string, w int) string {
	return strings.Repeat(" ", max(0, w-lipgloss.Width(s))) + s
}

// fit makes lines exactly n long, so panes keep their size.
func fit(lines []string, n int) []string {
	if len(lines) > n {
		return lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
