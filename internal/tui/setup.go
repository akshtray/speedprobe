package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/akshtray/speedprobe/internal/engine"
)

// Setup rows: one per provider, one per measurement, then the three numbers.
const numberRows = 3

func (m *model) rowCount() int { return len(m.providers) + len(engine.AllMeasurements) + numberRows }

func (m *model) updateSetup(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		m.cursor = (m.cursor + m.rowCount() - 1) % m.rowCount()
	case "down", "j", "tab":
		m.cursor = (m.cursor + 1) % m.rowCount()
	case "space", "x":
		m.toggle(m.cursor)
	case "left", "-", "h":
		m.adjust(m.cursor, -1)
	case "right", "+", "=", "l":
		m.adjust(m.cursor, +1)
	case "a":
		for i := range m.selected {
			m.selected[i] = true
		}
	case "enter":
		return m.startRun()
	}
	return m, nil
}

func (m *model) toggle(row int) {
	np := len(m.providers)
	switch {
	case row < np:
		m.selected[row] = !m.selected[row]
	case row < np+len(engine.AllMeasurements):
		m.measure[row-np] = !m.measure[row-np]
	default:
		m.adjust(row, +1)
	}
}

func (m *model) adjust(row, delta int) {
	np := len(m.providers)
	nm := len(engine.AllMeasurements)
	switch row {
	case np + nm:
		m.duration = min(max(m.duration+delta, 1), 60)
	case np + nm + 1:
		m.connections = min(max(m.connections+delta, 1), 32)
	case np + nm + 2:
		m.pings = min(max(m.pings+5*delta, 2), 200)
	default:
		if row < np+nm {
			m.toggle(row)
		}
	}
}

func (m *model) viewSetup() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n\n", styleTitle.Render("speedprobe "+m.version), styleDim.Render("multi-provider speed test"))

	row := 0
	line := func(text string) {
		prefix := "  "
		if row == m.cursor {
			prefix = styleCursor.Render("▸ ")
			text = styleCursor.Render(text)
		}
		b.WriteString(prefix + text + "\n")
		row++
	}
	box := func(on bool) string {
		if on {
			return "[x]"
		}
		return "[ ]"
	}

	b.WriteString(styleTitle.Render("Providers") + styleDim.Render("  space toggles, a selects all") + "\n")
	for i, p := range m.providers {
		info := p.Info()
		title := info.Title
		if info.Experimental {
			title += " " + styleWarn.Render("(experimental)")
		}
		if ok, why := p.Available(); !ok {
			title += " " + styleWarn.Render("("+why+")")
		}
		line(fmt.Sprintf("%s %-16s %s", box(m.selected[i]), title, styleDim.Render(info.Blurb)))
		if info.DataPolicy != "" {
			b.WriteString(styleDim.Render("        ↳ "+info.DataPolicy) + "\n")
		}
	}

	b.WriteString("\n" + styleTitle.Render("Measurements") + "\n")
	for i, meas := range engine.AllMeasurements {
		line(fmt.Sprintf("%s %s", box(m.measure[i]), meas))
	}

	b.WriteString("\n" + styleTitle.Render("Settings") + styleDim.Render("  ←/→ adjust") + "\n")
	line(fmt.Sprintf("duration     %3d s per direction %s", m.duration, styleDim.Render("(HTTP providers)")))
	line(fmt.Sprintf("connections  %3d parallel streams", m.connections))
	line(fmt.Sprintf("pings        %3d idle latency samples", m.pings))

	b.WriteString("\n")
	if m.flash != "" {
		b.WriteString(styleWarn.Render(m.flash) + "\n")
	}
	b.WriteString(styleKeys.Render("enter start · ↑/↓ move · space toggle · q quit"))
	return b.String()
}
