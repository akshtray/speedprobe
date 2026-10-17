package output

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/akshtray/speedprobe/internal/engine"
)

func TestThousands(t *testing.T) {
	for in, want := range map[float64]string{0: "0.0", 12.34: "12.3", 1234.56: "1,234.6", 1234567.8: "1,234,567.8", -1234.5: "-1,234.5"} {
		if got := thousands(in); got != want {
			t.Errorf("thousands(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestTable(t *testing.T) {
	r := engine.NewResult("cloudflare")
	r.Server = "Cloudflare SEA"
	r.LatencyMs, r.JitterMs = engine.F(12.3), engine.F(1.2)
	r.LoadedLatencyDownMs = engine.F(45)
	r.DownloadMbps, r.UploadMbps = engine.F(812.5), engine.F(1234.5)
	bad := engine.Failed("other", "timed out")
	var buf bytes.Buffer
	if err := Table(&buf, []*engine.Result{r, bad}, func(s string) string { return strings.ToUpper(s[:1]) + s[1:] }, time.Date(2026, 10, 16, 16, 30, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"speedprobe · 2026-10-16 16:30 UTC", "Cloudflare", "Cloudflare SEA", "12.3 ms", "45 / — ms", "812.5 Mbps", "1,234.5 Mbps", "Other", "timed out"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

func TestJSONAndLog(t *testing.T) {
	r := engine.NewResult("cloudflare")
	r.DownloadMbps = engine.F(1)
	var buf bytes.Buffer
	if err := JSON(&buf, []*engine.Result{r}, true); err != nil {
		t.Fatal(err)
	}
	var arr []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &arr); err != nil || len(arr) != 1 || arr[0]["provider"] != "cloudflare" {
		t.Fatalf("json: %v %s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "\n  ") {
		t.Fatal("expected indented output")
	}

	path := filepath.Join(t.TempDir(), "log.jsonl")
	for range 2 {
		if err := AppendLog(path, []*engine.Result{r}); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !json.Valid([]byte(lines[0])) {
		t.Fatalf("log = %q", data)
	}
}

func TestProgressNonTTYPrintsOutcomesOnly(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf, false)
	p.Handle(engine.Event{Kind: engine.RunStarted, Title: "Cloudflare"})
	p.Handle(engine.Event{Kind: engine.ThroughputSample, Title: "Cloudflare", Phase: engine.Download, Mbps: 10})
	ok := engine.NewResult("cloudflare")
	p.Handle(engine.Event{Kind: engine.RunFinished, Title: "Cloudflare", Result: ok})
	p.Handle(engine.Event{Kind: engine.RunFinished, Title: "Other", Result: engine.Failed("other", "x")})
	if got := buf.String(); got != "✓ Cloudflare\n✗ Other\n" {
		t.Fatalf("got %q", got)
	}
	buf.Reset()
	q := NewProgress(&buf, true)
	q.Handle(engine.Event{Kind: engine.RunFinished, Title: "Cloudflare", Result: ok})
	if buf.Len() != 0 {
		t.Fatalf("quiet wrote %q", buf.String())
	}
}
