// Package engine holds the measurement core of speedprobe: the vocabulary
// (measurements, options, results), the transfer loop shared by HTTP-based
// providers, and the event stream that renderers consume.
//
// The engine knows nothing about terminals. Progress is reported as Events
// (see ADR 0003); whoever runs the engine decides what to do with them.
package engine

import (
	"slices"
	"time"
)

// Measurement is one of the quantities a provider can be asked for.
type Measurement string

const (
	Latency  Measurement = "latency"
	Download Measurement = "download"
	Upload   Measurement = "upload"
)

// AllMeasurements lists every measurement in canonical order.
var AllMeasurements = []Measurement{Latency, Download, Upload}

// Kind says how speedprobe talks to a provider.
type Kind string

const (
	Native  Kind = "native"  // speedprobe speaks the provider's protocol itself
	Wrapped Kind = "wrapped" // speedprobe invokes a separately installed binary
)

// Info describes a provider to users and to the registry.
type Info struct {
	Name         string        // canonical id, e.g. "cloudflare"
	Title        string        // display name, e.g. "Cloudflare"
	URL          string        // where the provider lives on the web
	Aliases      []string      // alternative ids accepted on the command line
	Kind         Kind          // native or wrapped
	Experimental bool          // depends on an undocumented interface
	Capabilities []Measurement // measurements this provider can supply
	Blurb        string        // one-line summary of what it measures
	DataPolicy   string        // what the provider does with test data, if notable
}

// Supports reports whether the provider can supply m.
func (i Info) Supports(m Measurement) bool { return slices.Contains(i.Capabilities, m) }

// Options control a run. Zero values are not useful; start from DefaultOptions.
type Options struct {
	Measurements []Measurement // which measurements to take
	Duration     time.Duration // per direction, HTTP providers
	Connections  int           // parallel streams, HTTP providers
	Warmup       time.Duration // excluded from throughput (TCP ramp-up)
	Pings        int           // idle latency samples
}

// DefaultOptions mirrors the defaults of the Python prototype.
func DefaultOptions() Options {
	return Options{
		Measurements: slices.Clone(AllMeasurements),
		Duration:     10 * time.Second,
		Connections:  6,
		Warmup:       1500 * time.Millisecond,
		Pings:        20,
	}
}

// Wants reports whether m was requested.
func (o Options) Wants(m Measurement) bool { return slices.Contains(o.Measurements, m) }

// Provider is something speedprobe can run measurements against.
type Provider interface {
	Info() Info
	// Available reports whether the provider can be used on this machine and why not otherwise.
	Available() (ok bool, reason string)
	// Run takes the requested measurements and returns a Result. It must not
	// panic and should record failures on the Result rather than returning them.
	Run(ctx Context, opts Options, sink Sink) *Result
}

// EventKind classifies an Event.
type EventKind int

const (
	RunStarted       EventKind = iota // a provider's run has begun
	PhaseStarted                      // a measurement has begun
	LatencySample                     // one latency sample; Ms set. Phase == Latency with Index/Total for idle samples, else the transfer phase it was taken under
	ThroughputSample                  // live rate during a transfer; Phase/Mbps set
	PhaseFinished                     // a measurement has ended
	RunFinished                       // the run is over; Result set
	Status                            // free-text progress, e.g. "locating server"; Message set
)

// Event is one item on the progress stream.
type Event struct {
	Kind     EventKind
	Provider string // provider name
	Title    string // provider title, for display
	Phase    Measurement
	Index    int
	Total    int
	Ms       float64
	Mbps     float64
	Message  string
	Result   *Result
	Occurred time.Time
}

// Sink receives events. A nil Sink discards them.
type Sink func(Event)

// Emit sends e on s if s is non-nil, stamping the time.
func (s Sink) Emit(e Event) {
	if s == nil {
		return
	}
	if e.Occurred.IsZero() {
		e.Occurred = time.Now()
	}
	s(e)
}
