package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/akshtray/speedprobe/internal/engine"
)

// panel is the live state of one provider's run on the dashboard.
type panel struct {
	name, title string
	phase       engine.Measurement
	status      string
	started     bool
	done        bool
	idle        []float64 // idle latency samples, ms
	down, up    []float64 // throughput samples, Mbps
	loadedDown  []float64 // latency under download, ms
	loadedUp    []float64 // latency under upload, ms
	result      *engine.Result
}

func (p *panel) apply(e engine.Event) {
	switch e.Kind {
	case engine.RunStarted:
		p.started, p.status = true, "starting…"
	case engine.Status:
		p.status = e.Message
	case engine.PhaseStarted:
		p.phase, p.status = e.Phase, string(e.Phase)
	case engine.LatencySample:
		switch e.Phase {
		case engine.Latency:
			p.idle = append(p.idle, e.Ms)
		case engine.Download:
			p.loadedDown = append(p.loadedDown, e.Ms)
		case engine.Upload:
			p.loadedUp = append(p.loadedUp, e.Ms)
		}
	case engine.ThroughputSample:
		switch e.Phase {
		case engine.Download:
			p.down = append(p.down, e.Mbps)
		case engine.Upload:
			p.up = append(p.up, e.Mbps)
		}
	case engine.RunFinished:
		p.done, p.result = true, e.Result
		if e.Result != nil && e.Result.OK {
			p.status = "done"
		} else {
			p.status = "failed"
		}
	}
}

func (m *model) updateDashboard(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		if m.cancel != nil {
			m.cancel()
			return m, m.setFlash("stopping…")
		}
	}
	return m, nil
}

const (
	panelMinWidth = 34
	panelMaxWidth = 48
	sparkWidth    = 24
)

func (m *model) viewDashboard() string {
	var b strings.Builder
	active := "finishing"
	for i, p := range m.panels {
		if p.started && !p.done {
			active = fmt.Sprintf("%s (%d/%d)", p.title, i+1, len(m.panels))
			break
		}
	}
	fmt.Fprintf(&b, "%s  %s\n\n", styleTitle.Render("speedprobe "+m.version), styleDim.Render("running · "+active))

	n := len(m.panels)
	width := m.width
	if width <= 0 {
		width = 80
	}
	horizontal := n > 0 && width >= n*panelMinWidth
	pw := min(panelMaxWidth, width-1)
	if horizontal {
		pw = min(panelMaxWidth, width/n-1)
	}
	inner := max(pw-4, 20) // border and padding
	views := make([]string, 0, n)
	for _, p := range m.panels {
		views = append(views, renderPanel(p, inner))
	}
	if horizontal {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, views...))
	} else {
		b.WriteString(lipgloss.JoinVertical(lipgloss.Left, views...))
	}
	b.WriteString("\n")
	if m.flash != "" {
		b.WriteString(styleWarn.Render(m.flash) + "\n")
	}
	b.WriteString(styleKeys.Render("q stop · ctrl+c quit"))
	return b.String()
}

func renderPanel(p *panel, inner int) string {
	marker := styleDim.Render("·")
	switch {
	case p.done && p.result != nil && p.result.OK:
		marker = styleOK.Render("✓")
	case p.done:
		marker = styleBad.Render("✗")
	case p.started:
		marker = styleCursor.Render("●")
	}
	sw := min(sparkWidth, max(inner-20, 6))
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", marker, styleTitle.Render(p.title))
	b.WriteString(styleDim.Render(truncate(p.status, inner)) + "\n")

	b.WriteString(metricLine("ping", lastOrResult(p.idle, p.result, func(r *engine.Result) *float64 { return r.LatencyMs }), "ms", spark(p.idle, sw), styleLatency, inner) + "\n")
	b.WriteString(metricLine("↓ down", lastOrResult(p.down, p.result, func(r *engine.Result) *float64 { return r.DownloadMbps }), "Mbps", spark(p.down, sw), styleDown, inner) + "\n")
	b.WriteString(metricLine("  loaded", lastOrResult(p.loadedDown, p.result, func(r *engine.Result) *float64 { return r.LoadedLatencyDownMs }), "ms", spark(p.loadedDown, sw), styleLatency, inner) + "\n")
	b.WriteString(metricLine("↑ up", lastOrResult(p.up, p.result, func(r *engine.Result) *float64 { return r.UploadMbps }), "Mbps", spark(p.up, sw), styleUp, inner) + "\n")
	b.WriteString(metricLine("  loaded", lastOrResult(p.loadedUp, p.result, func(r *engine.Result) *float64 { return r.LoadedLatencyUpMs }), "ms", spark(p.loadedUp, sw), styleLatency, inner))
	if p.result != nil && p.result.Error != "" {
		b.WriteString("\n" + styleBad.Render(truncate(p.result.Error, inner)))
	} else if p.result != nil && p.result.Server != "" {
		b.WriteString("\n" + styleDim.Render(truncate(p.result.Server, inner)))
	}
	style := stylePanel
	if p.started && !p.done {
		style = styleActive
	}
	return style.Width(inner).Render(b.String())
}

// lastOrResult shows the live sample while running and the final figure once
// the result is in.
func lastOrResult(samples []float64, r *engine.Result, pick func(*engine.Result) *float64) *float64 {
	if r != nil {
		return pick(r)
	}
	if len(samples) == 0 {
		return nil
	}
	return &samples[len(samples)-1]
}

func metricLine(label string, v *float64, unit, sparkline string, style lipgloss.Style, inner int) string {
	val := "—"
	if v != nil {
		val = fmt.Sprintf("%.1f %s", *v, unit)
	}
	line := fmt.Sprintf("%-8s %12s ", label, val)
	return truncate(line, inner-lipgloss.Width(sparkline)) + style.Render(sparkline)
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}
