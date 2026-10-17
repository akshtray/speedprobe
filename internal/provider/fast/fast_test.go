package fast

import (
	"context"
	"testing"
	"time"

	"github.com/akshtray/speedprobe/internal/engine"
	"github.com/akshtray/speedprobe/internal/testserver"
)

func newProvider(t *testing.T, srvURL string) *Fast {
	t.Helper()
	t.Setenv(EnvBaseURL, srvURL)
	t.Setenv(EnvAPIURL, srvURL+"/netflix/speedtest/v2")
	return New(engine.NewHTTPClient("speedprobe/test"))
}

func TestRanged(t *testing.T) {
	got := ranged("https://host.example/speedtest?c=1", 100)
	if got != "https://host.example/speedtest/range/0-100?c=1" {
		t.Fatalf("ranged = %q", got)
	}
}

func TestRunAgainstTestServer(t *testing.T) {
	srv := testserver.Fast()
	defer srv.Close()
	f := newProvider(t, srv.URL)
	opts := engine.DefaultOptions()
	opts.Measurements = []engine.Measurement{engine.Latency, engine.Download}
	opts.Duration, opts.Warmup, opts.Connections, opts.Pings = 2*time.Second, 500*time.Millisecond, 2, 4

	var idle, throughput int
	res := f.Run(context.Background(), opts, func(e engine.Event) {
		switch {
		case e.Kind == engine.LatencySample && e.Phase == engine.Latency:
			idle++
		case e.Kind == engine.ThroughputSample:
			throughput++
		}
	})
	if !res.OK {
		t.Fatalf("run failed: %s", res.Error)
	}
	if res.Server != "Localhost, US" {
		t.Errorf("server = %q", res.Server)
	}
	if res.LatencyMs == nil || res.JitterMs == nil || res.DownloadMbps == nil || *res.DownloadMbps <= 0 || res.LoadedLatencyDownMs == nil {
		t.Errorf("values: lat=%v jit=%v down=%v loaded=%v", res.LatencyMs, res.JitterMs, res.DownloadMbps, res.LoadedLatencyDownMs)
	}
	if res.UploadMbps != nil {
		t.Errorf("upload must stay nil")
	}
	if res.Extra["isp"] != "Loopback ISP" || res.Extra["targets"] != 2 {
		t.Errorf("extra = %v", res.Extra)
	}
	if idle != 4 || throughput == 0 {
		t.Errorf("events: idle=%d throughput=%d", idle, throughput)
	}
}

func TestTokenScrapeFailureIsClear(t *testing.T) {
	srv := testserver.Cloudflare() // serves none of fast.com's paths
	defer srv.Close()
	f := newProvider(t, srv.URL)
	res := f.Run(context.Background(), engine.DefaultOptions(), nil)
	if res.OK || res.Error == "" {
		t.Fatalf("expected a failure, got %+v", res)
	}
}
