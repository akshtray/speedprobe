package tui

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/akshtray/speedprobe/internal/output"
)

func (m *model) updateResults(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "r", "enter":
		return m.startRun()
	case "b":
		m.screen = screenSetup
		return m, nil
	case "c":
		js, err := m.resultsJSON()
		if err != nil {
			return m, m.setFlash("could not encode JSON: " + err.Error())
		}
		return m, tea.Batch(tea.SetClipboard(js), m.setFlash("JSON copied to the clipboard (via OSC 52; your terminal must allow it)"))
	case "s":
		js, err := m.resultsJSON()
		if err != nil {
			return m, m.setFlash("could not encode JSON: " + err.Error())
		}
		name := fmt.Sprintf("speedprobe-%s.json", time.Now().Format("20060102-150405"))
		if err := os.WriteFile(name, []byte(js), 0o644); err != nil {
			return m, m.setFlash("could not save: " + err.Error())
		}
		return m, m.setFlash("saved " + name)
	}
	return m, nil
}

func (m *model) resultsJSON() (string, error) {
	var buf bytes.Buffer
	if err := output.JSON(&buf, m.results, true); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (m *model) viewResults() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n\n", styleTitle.Render("speedprobe "+m.version), styleDim.Render(fmt.Sprintf("results · round %d", m.round)))

	var table bytes.Buffer
	_ = output.Table(&table, m.results, m.reg.Title, time.Now())
	b.WriteString(table.String())
	b.WriteString("\n")

	failed := 0
	for _, r := range m.results {
		if !r.OK {
			failed++
		}
	}
	switch {
	case len(m.results) == 0:
		b.WriteString(styleWarn.Render("no results") + "\n")
	case failed > 0:
		b.WriteString(styleBad.Render(fmt.Sprintf("%d of %d providers failed", failed, len(m.results))) + "\n")
	default:
		b.WriteString(styleOK.Render("all providers succeeded") + "\n")
	}
	b.WriteString("\n")
	if m.flash != "" {
		b.WriteString(styleWarn.Render(m.flash) + "\n")
	}
	b.WriteString(styleKeys.Render("r run again · b back to setup · c copy JSON · s save JSON · q quit"))
	return b.String()
}
