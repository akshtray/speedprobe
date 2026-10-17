// Package output renders results for non-interactive use: a progress line on
// stderr, a table on stdout, JSON, and the JSONL log. It consumes engine
// events and never reaches back into the engine (ADR 0003).
package output

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/akshtray/speedprobe/internal/engine"
)

// IsTerminal reports whether w is an interactive terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// Progress writes a single updating status line to a terminal, or just the
// per-provider outcome lines when the writer is not a terminal.
type Progress struct {
	mu    sync.Mutex
	w     io.Writer
	tty   bool
	quiet bool
	dirty bool
}

// NewProgress returns a renderer for w. With quiet set, nothing is written.
func NewProgress(w io.Writer, quiet bool) *Progress {
	return &Progress{w: w, tty: IsTerminal(w), quiet: quiet}
}

// Handle consumes one event.
func (p *Progress) Handle(e engine.Event) {
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch e.Kind {
	case engine.RunStarted:
		p.status(e.Title + " starting…")
	case engine.Status:
		p.status(e.Title + " " + e.Message)
	case engine.LatencySample:
		if e.Phase == engine.Latency { // loaded-latency samples are for the dashboard
			p.status(fmt.Sprintf("%s latency %d/%d  %.1f ms", e.Title, e.Index, e.Total, e.Ms))
		}
	case engine.ThroughputSample:
		p.status(fmt.Sprintf("%s %s %9.1f Mbps", e.Title, e.Phase, e.Mbps))
	case engine.RunFinished:
		p.clear()
		if e.Result != nil && e.Result.OK {
			fmt.Fprintln(p.w, p.color("32", "✓")+" "+e.Title)
		} else {
			fmt.Fprintln(p.w, p.color("31", "✗")+" "+e.Title)
		}
	}
}

// Done clears any pending status line.
func (p *Progress) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clear()
}

// Note prints a dim informational line, e.g. between repeated rounds.
func (p *Progress) Note(msg string) {
	if p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clear()
	fmt.Fprintln(p.w, p.color("2", msg))
}

func (p *Progress) status(s string) {
	if !p.tty {
		return
	}
	fmt.Fprintf(p.w, "\r\033[K%s", s)
	p.dirty = true
}

func (p *Progress) clear() {
	if p.dirty {
		fmt.Fprint(p.w, "\r\033[K")
		p.dirty = false
	}
}

func (p *Progress) color(code, s string) string {
	if !p.tty {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}
