package ookla

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/akshtray/speedprobe/internal/engine"
)

// TestMain lets the test binary impersonate the speedtest CLI when asked.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FAKE_SPEEDTEST"); mode != "" {
		fakeSpeedtest(mode, os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

const fixture = `{"type":"log","timestamp":"2026-10-16T00:00:00Z","message":"Configuration - loading","level":"info"}
{"type":"result","timestamp":"2026-10-16T00:00:00Z","ping":{"jitter":1.25,"latency":12.5,"low":11,"high":14},` +
	`"download":{"bandwidth":31250000,"bytes":300000000,"elapsed":10000,"latency":{"iqm":45.1,"low":20,"high":90,"jitter":3}},` +
	`"upload":{"bandwidth":12500000,"bytes":120000000,"elapsed":10000,"latency":{"iqm":60.2}},"packetLoss":0.5,"isp":"Example ISP",` +
	`"server":{"id":1234,"host":"example.net","port":8080,"name":"Example Host","location":"Seattle, WA","country":"United States"},` +
	`"result":{"id":"abc","url":"https://www.speedtest.net/result/c/abc","persisted":true}}`

func fakeSpeedtest(mode string, args []string) {
	if slices.Contains(args, "--version") {
		if mode == "python" {
			fmt.Println("speedtest-cli 2.1.3")
		} else {
			fmt.Println("Speedtest by Ookla 1.2.0.84 (ea6b6773cf) Linux/x86_64-linux-musl 5.15.0 x86_64")
		}
		return
	}
	switch mode {
	case "ok":
		fmt.Println(fixture)
	case "license":
		fmt.Fprintln(os.Stderr, `{"type":"log","message":"To accept the message please run speedtest interactively or use the following: speedtest --accept-license","level":"error"}`)
		os.Exit(1)
	case "echo-args":
		fmt.Println(`{"type":"log","message":"` + strings.Join(args, " ") + `"}`)
		os.Exit(2)
	}
}

func fake(t *testing.T, mode string, cfg Config) *Ookla {
	t.Helper()
	t.Setenv(EnvBinary, os.Args[0])
	t.Setenv("FAKE_SPEEDTEST", mode)
	return New(cfg)
}

func TestParsesResult(t *testing.T) {
	o := fake(t, "ok", Config{})
	if ok, why := o.Available(); !ok || !strings.HasPrefix(why, "ookla (") {
		t.Fatalf("available = %v %q", ok, why)
	}
	var status int
	res := o.Run(context.Background(), engine.DefaultOptions(), func(e engine.Event) {
		if e.Kind == engine.Status {
			status++
		}
	})
	if !res.OK {
		t.Fatalf("run failed: %s", res.Error)
	}
	if res.Server != "Example Host (Seattle, WA) #1234" {
		t.Errorf("server = %q", res.Server)
	}
	if *res.LatencyMs != 12.5 || *res.JitterMs != 1.25 || *res.DownloadMbps != 250 || *res.UploadMbps != 100 {
		t.Errorf("numbers: %v %v %v %v", *res.LatencyMs, *res.JitterMs, *res.DownloadMbps, *res.UploadMbps)
	}
	if *res.LoadedLatencyDownMs != 45.1 || *res.LoadedLatencyUpMs != 60.2 || *res.PacketLossPct != 0.5 {
		t.Errorf("loaded/loss: %v %v %v", res.LoadedLatencyDownMs, res.LoadedLatencyUpMs, res.PacketLossPct)
	}
	if res.Extra["isp"] != "Example ISP" || res.Extra["result_url"] != "https://www.speedtest.net/result/c/abc" {
		t.Errorf("extra = %v", res.Extra)
	}
	if res.Note != "" || status != 1 {
		t.Errorf("note=%q status events=%d", res.Note, status)
	}
}

func TestSubsetAddsNote(t *testing.T) {
	o := fake(t, "ok", Config{})
	opts := engine.DefaultOptions()
	opts.Measurements = []engine.Measurement{engine.Download}
	res := o.Run(context.Background(), opts, nil)
	if !res.OK || res.Note != "Ookla CLI always runs every measurement" {
		t.Fatalf("note = %q ok = %v", res.Note, res.OK)
	}
}

func TestLicenseHint(t *testing.T) {
	res := fake(t, "license", Config{}).Run(context.Background(), engine.DefaultOptions(), nil)
	if res.OK || !strings.Contains(res.Error, "--accept-ookla-license") {
		t.Fatalf("error = %q", res.Error)
	}
}

func TestFlagsArePassedThrough(t *testing.T) {
	res := fake(t, "echo-args", Config{ServerID: 42, AcceptLicense: true}).Run(context.Background(), engine.DefaultOptions(), nil)
	if res.OK {
		t.Fatal("echo-args mode exits non-zero without a result")
	}
	for _, want := range []string{"--format=json", "--progress=no", "--accept-license", "--accept-gdpr", "--server-id 42"} {
		if !strings.Contains(res.Error, want) {
			t.Errorf("args missing %q: %s", want, res.Error)
		}
	}
}

func TestPythonSpeedtestCLIIsRejected(t *testing.T) {
	ok, why := fake(t, "python", Config{}).Available()
	if ok || !strings.Contains(why, "python speedtest-cli") {
		t.Fatalf("available = %v %q", ok, why)
	}
}

func TestMissingBinary(t *testing.T) {
	t.Setenv(EnvBinary, "/nonexistent/speedtest")
	o := New(Config{})
	ok, why := o.Available()
	if ok || !strings.Contains(why, "speedtest.net/apps/cli") || strings.Contains(why, "24.04") {
		t.Fatalf("available = %v %q (short form expected)", ok, why)
	}
	res := o.Run(context.Background(), engine.DefaultOptions(), nil)
	if res.OK || !strings.Contains(res.Error, "24.04") {
		t.Fatalf("run error should carry the full hint: %q", res.Error)
	}
}
