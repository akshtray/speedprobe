package mlab

import (
	"context"
	"testing"
	"time"

	"github.com/akshtray/speedprobe/internal/engine"
	"github.com/akshtray/speedprobe/internal/testserver"
)

func newProvider(t *testing.T, locate string, idx int) *MLab {
	t.Helper()
	t.Setenv(EnvLocateURL, locate)
	return New(engine.NewHTTPClient("speedprobe/test"), "test", Config{ServerIndex: idx})
}

func TestRunAgainstTestServer(t *testing.T) {
	srv := testserver.MLab()
	defer srv.Close()
	m := newProvider(t, srv.URL+"/locate", 0)

	var down, up int
	sink := engine.Sink(func(e engine.Event) {
		if e.Kind == engine.ThroughputSample {
			switch e.Phase {
			case engine.Download:
				down++
			case engine.Upload:
				up++
			}
		}
	})
	res := m.Run(context.Background(), engine.DefaultOptions(), sink)
	if !res.OK {
		t.Fatalf("run failed: %s", res.Error)
	}
	if res.Server != "mlab-mock (Localhost, US)" {
		t.Errorf("server = %q", res.Server)
	}
	if res.DownloadMbps == nil || *res.DownloadMbps <= 0 || res.UploadMbps == nil || *res.UploadMbps <= 0 {
		t.Errorf("throughput: down=%v up=%v", res.DownloadMbps, res.UploadMbps)
	}
	// The test server reports MinRTT 12000 µs, RTT 25000 µs, RTTVar 3000 µs.
	if res.LatencyMs == nil || *res.LatencyMs != 12 || res.JitterMs == nil || *res.JitterMs != 3 {
		t.Errorf("latency=%v jitter=%v", res.LatencyMs, res.JitterMs)
	}
	if res.LoadedLatencyDownMs == nil || *res.LoadedLatencyDownMs != 25 || res.LoadedLatencyUpMs == nil || *res.LoadedLatencyUpMs != 25 {
		t.Errorf("loaded: down=%v up=%v", res.LoadedLatencyDownMs, res.LoadedLatencyUpMs)
	}
	if c, ok := res.Extra["candidates"].([]string); !ok || len(c) != 1 || c[0] != "mlab-mock" {
		t.Errorf("candidates = %v", res.Extra["candidates"])
	}
	if res.Extra["uuid"] != "mock-uuid" {
		t.Errorf("uuid = %v", res.Extra["uuid"])
	}
	if down == 0 || up == 0 {
		t.Errorf("throughput events: down=%d up=%d", down, up)
	}
}

func TestLatencyOnlyStillDownloadsButReportsNoThroughput(t *testing.T) {
	srv := testserver.MLab()
	defer srv.Close()
	m := newProvider(t, srv.URL+"/locate", 0)
	opts := engine.DefaultOptions()
	opts.Measurements = []engine.Measurement{engine.Latency}
	res := m.Run(context.Background(), opts, nil)
	if !res.OK || res.LatencyMs == nil || res.DownloadMbps != nil || res.UploadMbps != nil || res.LoadedLatencyDownMs != nil {
		t.Fatalf("unexpected: ok=%v lat=%v down=%v up=%v err=%q", res.OK, res.LatencyMs, res.DownloadMbps, res.UploadMbps, res.Error)
	}
}

func TestFallsBackToNextTargetAndHonoursServerIndex(t *testing.T) {
	srv := testserver.MLabWithDeadFirstTarget()
	defer srv.Close()
	opts := engine.DefaultOptions()
	opts.Measurements = []engine.Measurement{engine.Download}

	res := newProvider(t, srv.URL+"/locate", 0).Run(context.Background(), opts, nil)
	if !res.OK || res.Server != "mlab-mock (Localhost, US)" {
		t.Fatalf("fallback: ok=%v server=%q err=%q", res.OK, res.Server, res.Error)
	}
	if c, _ := res.Extra["candidates"].([]string); len(c) != 2 {
		t.Errorf("candidates = %v", res.Extra["candidates"])
	}
	start := time.Now()
	res = newProvider(t, srv.URL+"/locate", 1).Run(context.Background(), opts, nil)
	if !res.OK || res.Server != "mlab-mock (Localhost, US)" {
		t.Fatalf("index 1: ok=%v server=%q err=%q", res.OK, res.Server, res.Error)
	}
	if time.Since(start) > 8*time.Second {
		t.Errorf("index 1 should skip the dead target entirely")
	}
}

func TestLocateFailureIsReported(t *testing.T) {
	m := newProvider(t, "http://127.0.0.1:1/locate", 0)
	res := m.Run(context.Background(), engine.DefaultOptions(), nil)
	if res.OK || res.Error == "" || res.LatencyMs != nil {
		t.Fatalf("expected failure: %+v", res)
	}
}
