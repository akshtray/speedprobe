package cloudflare

import (
	"context"
	"testing"
	"time"

	"github.com/akshtray/speedprobe/internal/engine"
	"github.com/akshtray/speedprobe/internal/testserver"
)

func TestRunAgainstTestServer(t *testing.T) {
	srv := testserver.Cloudflare()
	defer srv.Close()
	t.Setenv(EnvBaseURL, srv.URL)

	cf := New(engine.NewHTTPClient("speedprobe/test"))
	opts := engine.DefaultOptions()
	opts.Duration = 2 * time.Second
	opts.Warmup = 500 * time.Millisecond
	opts.Connections = 2
	opts.Pings = 5

	var latencySamples, throughput int
	sink := engine.Sink(func(e engine.Event) {
		switch e.Kind {
		case engine.LatencySample:
			if e.Phase == engine.Latency {
				latencySamples++
			}
		case engine.ThroughputSample:
			throughput++
		}
	})
	res := cf.Run(context.Background(), opts, sink)

	if !res.OK {
		t.Fatalf("run failed: %s", res.Error)
	}
	if res.Server != "Cloudflare SEA" {
		t.Errorf("server = %q", res.Server)
	}
	if res.LatencyMs == nil || *res.LatencyMs < 0 || res.JitterMs == nil {
		t.Errorf("latency = %v jitter = %v", res.LatencyMs, res.JitterMs)
	}
	if res.DownloadMbps == nil || *res.DownloadMbps <= 0 {
		t.Errorf("download = %v", res.DownloadMbps)
	}
	if res.UploadMbps == nil || *res.UploadMbps <= 0 {
		t.Errorf("upload = %v", res.UploadMbps)
	}
	if res.LoadedLatencyDownMs == nil || res.LoadedLatencyUpMs == nil {
		t.Errorf("loaded latency missing: %v %v", res.LoadedLatencyDownMs, res.LoadedLatencyUpMs)
	}
	if res.Extra["clientIp"] != "127.0.0.1" {
		t.Errorf("extra = %v", res.Extra)
	}
	if latencySamples != 5 || throughput == 0 {
		t.Errorf("events: latency=%d throughput=%d", latencySamples, throughput)
	}
}

func TestOnlyRequestedMeasurements(t *testing.T) {
	srv := testserver.Cloudflare()
	defer srv.Close()
	t.Setenv(EnvBaseURL, srv.URL)

	cf := New(engine.NewHTTPClient("speedprobe/test"))
	opts := engine.DefaultOptions()
	opts.Measurements = []engine.Measurement{engine.Latency}
	opts.Pings = 3
	res := cf.Run(context.Background(), opts, nil)
	if !res.OK || res.LatencyMs == nil || res.DownloadMbps != nil || res.UploadMbps != nil {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestUnreachableEndpointFailsCleanly(t *testing.T) {
	t.Setenv(EnvBaseURL, "http://127.0.0.1:1")
	cf := New(engine.NewHTTPClient("speedprobe/test"))
	opts := engine.DefaultOptions()
	opts.Duration = 200 * time.Millisecond
	opts.Pings = 2
	res := cf.Run(context.Background(), opts, nil)
	if res.OK || res.Error == "" {
		t.Fatalf("expected failure, got %+v", res)
	}
	if res.LatencyMs != nil || res.DownloadMbps != nil {
		t.Fatalf("values must be nil on failure: %+v", res)
	}
}

func TestTraceFallbackWhenMetaIsForbidden(t *testing.T) {
	srv := testserver.CloudflareWithoutMeta()
	defer srv.Close()
	t.Setenv(EnvBaseURL, srv.URL)

	cf := New(engine.NewHTTPClient("speedprobe/test"))
	opts := engine.DefaultOptions()
	opts.Measurements = []engine.Measurement{engine.Latency}
	opts.Pings = 2
	res := cf.Run(context.Background(), opts, nil)
	if !res.OK {
		t.Fatalf("run failed: %s", res.Error)
	}
	if res.Extra["clientIp"] != "127.0.0.2" || res.Extra["country"] != "US" {
		t.Errorf("extra from trace = %v", res.Extra)
	}
	if _, ok := res.Extra["asOrganization"]; ok {
		t.Errorf("meta data should be absent when /meta is forbidden: %v", res.Extra)
	}
	if res.Server != "Cloudflare SEA" {
		t.Errorf("server = %q", res.Server)
	}
}
