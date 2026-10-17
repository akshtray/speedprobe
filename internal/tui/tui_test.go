package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/akshtray/speedprobe/internal/engine"
	"github.com/akshtray/speedprobe/internal/provider"
	"github.com/akshtray/speedprobe/internal/provider/cloudflare"
	"github.com/akshtray/speedprobe/internal/testserver"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	}
	r := []rune(s)
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

func newTestModel(t *testing.T) *model {
	t.Helper()
	m := newModel(context.Background(), Options{Version: "test", Registry: provider.New("test", provider.Config{})})
	m.width, m.height = 100, 40
	return m
}

func TestSpark(t *testing.T) {
	if spark(nil, 10) != "" || spark([]float64{1}, 0) != "" {
		t.Fatal("empty inputs must draw nothing")
	}
	s := spark([]float64{0, 50, 100}, 10)
	if s != "▁▄█" {
		t.Fatalf("spark = %q", s)
	}
	if got := spark([]float64{1, 2, 3, 4, 5}, 2); len([]rune(got)) != 2 {
		t.Fatalf("width not honoured: %q", got)
	}
	if spark([]float64{0, 0}, 5) != "▁▁" {
		t.Fatalf("all-zero should be flat: %q", spark([]float64{0, 0}, 5))
	}
}

func TestSetupNavigationAndToggles(t *testing.T) {
	m := newTestModel(t)
	if !m.selected[0] || m.selected[1] {
		t.Fatalf("default selection should be the first provider only: %v", m.selected)
	}
	m.Update(key("down"))
	m.Update(key("space"))
	if !m.selected[1] {
		t.Fatal("space should toggle the second provider")
	}
	// Move to the duration row and adjust it.
	for m.cursor != len(m.providers)+len(engine.AllMeasurements) {
		m.Update(key("down"))
	}
	m.Update(key("right"))
	m.Update(key("right"))
	m.Update(key("left"))
	if m.duration != 11 {
		t.Fatalf("duration = %d, want 11", m.duration)
	}
	opts := m.options()
	if opts.Duration != 11*time.Second || len(opts.Measurements) != 3 || len(m.chosen()) != 2 {
		t.Fatalf("options = %+v chosen = %d", opts, len(m.chosen()))
	}
	view := m.viewSetup()
	t.Log("\n" + view)
	for _, want := range []string{"Cloudflare", "M-Lab", "publishes every test", "duration", "enter start"} {
		if !strings.Contains(view, want) {
			t.Errorf("setup view missing %q", want)
		}
	}
}

func TestStartRefusesEmptySelection(t *testing.T) {
	m := newTestModel(t)
	m.selected[0] = false
	_, cmd := m.Update(key("enter"))
	if m.screen != screenSetup || m.flash == "" || cmd == nil {
		t.Fatalf("expected to stay on setup with a flash, screen=%v flash=%q", m.screen, m.flash)
	}
	m.selected[0] = true
	for i := range m.measure {
		m.measure[i] = false
	}
	m.Update(key("enter"))
	if m.screen != screenSetup || !strings.Contains(m.flash, "measurement") {
		t.Fatalf("expected measurement flash, got screen=%v flash=%q", m.screen, m.flash)
	}
}

// drive pumps run messages through Update until the results screen appears.
func drive(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for cmd != nil && m.screen == screenDashboard {
		select {
		case <-deadline:
			t.Fatal("run did not finish")
		default:
		}
		msg := cmd()
		if msg == nil {
			return
		}
		_, cmd = m.Update(msg)
	}
}

func TestRunFlowAgainstTestServer(t *testing.T) {
	srv := testserver.Cloudflare()
	defer srv.Close()
	t.Setenv(cloudflare.EnvBaseURL, srv.URL)

	m := newTestModel(t)
	m.duration, m.connections, m.pings = 1, 2, 3
	_, cmd := m.Update(key("enter"))
	if m.screen != screenDashboard || len(m.panels) != 1 || cmd == nil {
		t.Fatalf("start: screen=%v panels=%d", m.screen, len(m.panels))
	}
	drive(t, m, cmd)

	if m.screen != screenResults || len(m.results) != 1 || !m.results[0].OK {
		t.Fatalf("after run: screen=%v results=%+v", m.screen, m.results)
	}
	p := m.panels[0]
	if !p.done || len(p.idle) != 3 || len(p.down) == 0 || len(p.up) == 0 || len(p.loadedDown) == 0 {
		t.Fatalf("panel samples: idle=%d down=%d up=%d loadedDown=%d", len(p.idle), len(p.down), len(p.up), len(p.loadedDown))
	}
	dash := m.viewDashboard()
	t.Log("\n" + dash)
	if !strings.Contains(dash, "Cloudflare") || !strings.Contains(dash, "Mbps") {
		t.Errorf("dashboard view:\n%s", dash)
	}
	res := m.viewResults()
	t.Log("\n" + res)
	if !strings.Contains(res, "Cloudflare SEA") || !strings.Contains(res, "all providers succeeded") {
		t.Errorf("results view:\n%s", res)
	}
	js, err := m.resultsJSON()
	if err != nil || !strings.Contains(js, `"provider": "cloudflare"`) {
		t.Errorf("json: %v %s", err, js)
	}
	m.Update(key("b"))
	if m.screen != screenSetup {
		t.Fatal("b should return to setup")
	}
}

func TestStopDuringRunLandsOnResults(t *testing.T) {
	srv := testserver.Cloudflare()
	defer srv.Close()
	t.Setenv(cloudflare.EnvBaseURL, srv.URL)

	m := newTestModel(t)
	m.duration, m.connections, m.pings = 10, 2, 3
	_, cmd := m.Update(key("enter"))
	_, _ = m.Update(key("q")) // cancels the run context
	drive(t, m, cmd)
	if m.screen != screenResults || len(m.results) != 1 {
		t.Fatalf("after stop: screen=%v results=%d", m.screen, len(m.results))
	}
}
