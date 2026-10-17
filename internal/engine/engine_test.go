package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMedianAndJitter(t *testing.T) {
	if Median(nil) != nil {
		t.Fatal("median of nothing should be nil")
	}
	if m := *Median([]float64{3, 1, 2}); m != 2 {
		t.Fatalf("median = %v, want 2", m)
	}
	if m := *Median([]float64{4, 1, 3, 2}); m != 2.5 {
		t.Fatalf("median = %v, want 2.5", m)
	}
	if Jitter([]float64{1}) != nil {
		t.Fatal("jitter of one sample should be nil")
	}
	if j := *Jitter([]float64{10, 12, 9}); j != 2.5 {
		t.Fatalf("jitter = %v, want 2.5", j)
	}
}

func TestMeterSkipsWarmup(t *testing.T) {
	m := NewMeter(50 * time.Millisecond)
	m.Add(1_000_000) // during warm-up: must not count toward the steady-state rate
	time.Sleep(60 * time.Millisecond)
	m.Add(0) // closes the warm-up window
	time.Sleep(100 * time.Millisecond)
	m.Add(125_000) // 1 Mbit after warm-up over ~0.1 s = ~10 Mbps
	got := m.Mbps()
	if got < 5 || got > 15 {
		t.Fatalf("steady-state Mbps = %.2f, want about 10", got)
	}
	if m.Total() != 1_125_000 {
		t.Fatalf("total = %d", m.Total())
	}
}

func TestTransferRunsWorkersAndProbes(t *testing.T) {
	opts := DefaultOptions()
	opts.Duration = 300 * time.Millisecond
	opts.Warmup = 0
	opts.Connections = 3
	worker := func(ctx context.Context, i int, m *Meter, deadline time.Time) error {
		for time.Now().Before(deadline) {
			m.Add(1000)
			time.Sleep(10 * time.Millisecond)
		}
		return nil
	}
	probe := func(ctx context.Context) (float64, error) { return 7, nil }
	var samples int
	sink := Sink(func(e Event) {
		if e.Kind == ThroughputSample && e.Phase == Download {
			samples++
		}
	})
	mbps, loaded, err := Transfer(context.Background(), Download, "p", "P", worker, opts, sink, probe)
	if err != nil {
		t.Fatal(err)
	}
	if mbps <= 0 {
		t.Fatalf("mbps = %v", mbps)
	}
	if loaded == nil || *loaded != 7 {
		t.Fatalf("loaded = %v, want 7", loaded)
	}
	if samples == 0 {
		t.Fatal("no throughput samples emitted")
	}
}

func TestTransferReportsErrorOnlyWhenNothingMoved(t *testing.T) {
	opts := DefaultOptions()
	opts.Duration = 50 * time.Millisecond
	boom := errors.New("boom")
	failing := func(ctx context.Context, i int, m *Meter, deadline time.Time) error { return boom }
	if _, _, err := Transfer(context.Background(), Upload, "p", "P", failing, opts, nil, nil); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	partial := func(ctx context.Context, i int, m *Meter, deadline time.Time) error {
		m.Add(10)
		return boom
	}
	if _, _, err := Transfer(context.Background(), Upload, "p", "P", partial, opts, nil, nil); err != nil {
		t.Fatalf("err = %v, want nil when bytes moved", err)
	}
}

func TestResultFailCollapsesIdenticalErrors(t *testing.T) {
	r := NewResult("x")
	r.Fail("download", context.DeadlineExceeded)
	r.Fail("upload", context.DeadlineExceeded)
	r.Fail("latency", &HTTPError{Status: "503 Service Unavailable", Code: 503})
	want := "download/upload: timed out; latency: HTTP 503 Service Unavailable"
	if r.Error != want || r.OK {
		t.Fatalf("error = %q ok=%v", r.Error, r.OK)
	}
}

// The JSON shape is a contract (ADR 0002): every key present, nulls for
// missing values, floats rounded to two decimals, schema stamped.
func TestResultJSONContract(t *testing.T) {
	r := NewResult("cloudflare")
	r.Server = "Cloudflare SEA"
	r.LatencyMs = F(12.3456)
	r.DownloadMbps = F(812.999)
	r.Timestamp = time.Date(2026, 10, 16, 23, 39, 53, 0, time.UTC)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	keys := []string{"provider", "ok", "server", "latency_ms", "jitter_ms", "loaded_latency_down_ms",
		"loaded_latency_up_ms", "download_mbps", "upload_mbps", "packet_loss_pct", "note", "error", "extra", "timestamp", "schema"}
	for _, k := range keys {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	if len(got) != len(keys) {
		t.Errorf("got %d keys, want %d: %s", len(got), len(keys), b)
	}
	if got["latency_ms"] != 12.35 || got["download_mbps"] != 813.0 {
		t.Errorf("rounding: %s", b)
	}
	if got["jitter_ms"] != nil || got["note"] != nil || got["error"] != nil {
		t.Errorf("absent values must be null: %s", b)
	}
	if got["schema"] != float64(Schema) || got["ok"] != true {
		t.Errorf("schema/ok: %s", b)
	}
	if got["timestamp"] != "2026-10-16T23:39:53+00:00" {
		t.Errorf("timestamp = %v", got["timestamp"])
	}
	if _, ok := got["extra"].(map[string]any); !ok {
		t.Errorf("extra must be an object: %s", b)
	}
	if !strings.HasPrefix(string(b), `{"provider":"cloudflare","ok":true`) {
		t.Errorf("field order changed: %s", b)
	}
}

type fakeProvider struct {
	info  Info
	avail bool
	ran   int
	got   Options
}

func (f *fakeProvider) Info() Info { return f.info }
func (f *fakeProvider) Available() (bool, string) {
	if f.avail {
		return true, "ready"
	}
	return false, "not installed"
}
func (f *fakeProvider) Run(ctx Context, opts Options, sink Sink) *Result {
	f.ran++
	f.got = opts
	r := NewResult(f.info.Name)
	r.DownloadMbps = F(1)
	r.Note = "own note"
	return r
}

func TestRunAllNarrowsToCapabilities(t *testing.T) {
	a := &fakeProvider{info: Info{Name: "a", Title: "A", Capabilities: []Measurement{Latency}}, avail: true}
	results := RunAll(context.Background(), []Provider{a}, DefaultOptions(), nil)
	if a.ran != 1 || len(a.got.Measurements) != 1 || a.got.Measurements[0] != Latency {
		t.Fatalf("provider should only be asked for latency, got %v", a.got.Measurements)
	}
	if results[0].Note != "own note; download/upload not supported by A" || !results[0].OK {
		t.Fatalf("note = %q ok = %v", results[0].Note, results[0].OK)
	}
	// Nothing supported at all: no run, empty successful result with a note.
	opts := DefaultOptions()
	opts.Measurements = []Measurement{Upload}
	results = RunAll(context.Background(), []Provider{a}, opts, nil)
	if a.ran != 1 || !results[0].OK || results[0].Note != "upload not supported by A" || results[0].DownloadMbps != nil {
		t.Fatalf("unsupported-only: ran=%d result=%+v", a.ran, results[0])
	}
}

func TestRunAllIsSequentialAndSkipsUnavailable(t *testing.T) {
	a := &fakeProvider{info: Info{Name: "a", Title: "A", Capabilities: AllMeasurements}, avail: true}
	b := &fakeProvider{info: Info{Name: "b", Title: "B"}}
	var order []EventKind
	results := RunAll(context.Background(), []Provider{a, b}, DefaultOptions(), func(e Event) { order = append(order, e.Kind) })
	if len(results) != 2 || !results[0].OK || results[1].OK || results[1].Error != "not installed" {
		t.Fatalf("results = %+v", results)
	}
	if a.ran != 1 || b.ran != 0 {
		t.Fatalf("ran a=%d b=%d", a.ran, b.ran)
	}
	if len(order) != 2 || order[0] != RunStarted || order[1] != RunFinished {
		t.Fatalf("events = %v", order)
	}
}
