// Package tui is the full-screen interactive front end: a setup screen, a
// live dashboard while measurements run, and a results screen. It consumes
// engine events exactly like the plain renderer does (ADR 0003) and never
// calls a provider directly.
package tui

import (
	"context"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/akshtray/speedprobe/internal/engine"
	"github.com/akshtray/speedprobe/internal/provider"
)

// Options configure a TUI session.
type Options struct {
	Version  string
	Registry *provider.Registry
	Input    io.Reader // nil means the program's default (stdin)
	Output   io.Writer // nil means the program's default (stdout)
}

// Run shows the TUI until the user quits. ctx cancellation ends it too.
func Run(ctx context.Context, o Options) error {
	popts := []tea.ProgramOption{tea.WithContext(ctx)}
	if o.Input != nil {
		popts = append(popts, tea.WithInput(o.Input))
	}
	if o.Output != nil {
		popts = append(popts, tea.WithOutput(o.Output))
	}
	p := tea.NewProgram(newModel(ctx, o), popts...)
	_, err := p.Run()
	return err
}

type screen int

const (
	screenSetup screen = iota
	screenDashboard
	screenResults
)

// eventMsg carries one engine event into the Bubble Tea loop.
type eventMsg struct{ e engine.Event }

// doneMsg carries the results when the engine has finished a round.
type doneMsg struct{ results []*engine.Result }

// clearFlashMsg hides the transient status line.
type clearFlashMsg struct{}

type model struct {
	parent  context.Context
	version string
	reg     *provider.Registry

	// setup
	providers   []engine.Provider
	selected    []bool
	measure     []bool // parallel to engine.AllMeasurements
	duration    int    // seconds per direction
	connections int
	pings       int
	cursor      int

	// run
	screen  screen
	width   int
	height  int
	flash   string
	cancel  context.CancelFunc
	events  <-chan tea.Msg
	panels  []*panel
	results []*engine.Result
	round   int
}

func newModel(parent context.Context, o Options) *model {
	providers := o.Registry.All()
	selected := make([]bool, len(providers))
	if len(selected) > 0 {
		selected[0] = true // Cloudflare, the same default as `speedprobe run`
	}
	measure := make([]bool, len(engine.AllMeasurements))
	for i := range measure {
		measure[i] = true
	}
	d := engine.DefaultOptions()
	return &model{
		parent:      parent,
		version:     o.Version,
		reg:         o.Registry,
		providers:   providers,
		selected:    selected,
		measure:     measure,
		duration:    int(d.Duration.Seconds()),
		connections: d.Connections,
		pings:       d.Pings,
	}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case clearFlashMsg:
		m.flash = ""
		return m, nil
	case eventMsg:
		m.apply(msg.e)
		return m, waitEvent(m.events)
	case doneMsg:
		m.results = msg.results
		if m.cancel != nil {
			m.cancel()
			m.cancel = nil
		}
		m.screen = screenResults
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}
		switch m.screen {
		case screenSetup:
			return m.updateSetup(msg)
		case screenDashboard:
			return m.updateDashboard(msg)
		case screenResults:
			return m.updateResults(msg)
		}
	}
	return m, nil
}

func (m *model) View() tea.View {
	var content string
	switch m.screen {
	case screenSetup:
		content = m.viewSetup()
	case screenDashboard:
		content = m.viewDashboard()
	case screenResults:
		content = m.viewResults()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "speedprobe"
	return v
}

// chosen returns the providers ticked on the setup screen, in display order.
func (m *model) chosen() []engine.Provider {
	var out []engine.Provider
	for i, p := range m.providers {
		if m.selected[i] {
			out = append(out, p)
		}
	}
	return out
}

func (m *model) options() engine.Options {
	opts := engine.DefaultOptions()
	opts.Measurements = opts.Measurements[:0]
	for i, on := range m.measure {
		if on {
			opts.Measurements = append(opts.Measurements, engine.AllMeasurements[i])
		}
	}
	opts.Duration = secondsToDuration(m.duration)
	opts.Connections = m.connections
	opts.Pings = m.pings
	return opts
}

// startRun launches the engine in a goroutine and begins consuming events.
func (m *model) startRun() (tea.Model, tea.Cmd) {
	providers := m.chosen()
	if len(providers) == 0 {
		return m, m.setFlash("pick at least one provider")
	}
	opts := m.options()
	if len(opts.Measurements) == 0 {
		return m, m.setFlash("pick at least one measurement")
	}
	ctx, cancel := context.WithCancel(m.parent)
	m.cancel = cancel
	m.panels = m.panels[:0]
	for _, p := range providers {
		info := p.Info()
		m.panels = append(m.panels, &panel{name: info.Name, title: info.Title})
	}
	m.results = nil
	m.round++
	m.screen = screenDashboard
	m.flash = ""

	ch := make(chan tea.Msg, 1024)
	m.events = ch
	go func() {
		results := engine.RunAll(ctx, providers, opts, func(e engine.Event) { ch <- eventMsg{e} })
		ch <- doneMsg{results}
		close(ch)
	}()
	return m, waitEvent(ch)
}

// waitEvent returns a command that delivers the next message from the run.
func waitEvent(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *model) apply(e engine.Event) {
	for _, p := range m.panels {
		if p.name == e.Provider {
			p.apply(e)
			return
		}
	}
}

func (m *model) setFlash(s string) tea.Cmd {
	m.flash = s
	return tea.Tick(flashDuration, func(time.Time) tea.Msg { return clearFlashMsg{} })
}
