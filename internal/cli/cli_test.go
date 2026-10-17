package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/akshtray/speedprobe/internal/provider/cloudflare"
	"github.com/akshtray/speedprobe/internal/provider/mlab"
	"github.com/akshtray/speedprobe/internal/testserver"
)

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main("test", args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestRunJSONAgainstTestServer(t *testing.T) {
	srv := testserver.Cloudflare()
	defer srv.Close()
	t.Setenv(cloudflare.EnvBaseURL, srv.URL)

	code, out, errOut := run(t, "run", "-p", "cf", "-d", "1", "-c", "2", "--pings", "3", "--json", "-q")
	if code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if errOut != "" {
		t.Errorf("quiet run wrote to stderr: %q", errOut)
	}
	var results []map[string]any
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if len(results) != 1 || results[0]["provider"] != "cloudflare" || results[0]["ok"] != true {
		t.Fatalf("results = %v", results)
	}
	if results[0]["download_mbps"].(float64) <= 0 || results[0]["schema"].(float64) != 1 {
		t.Fatalf("results = %v", results)
	}
}

func TestRunTableAndMeasurementSubset(t *testing.T) {
	srv := testserver.Cloudflare()
	defer srv.Close()
	t.Setenv(cloudflare.EnvBaseURL, srv.URL)

	code, out, errOut := run(t, "run", "-t", "latency", "--pings", "3")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "Cloudflare SEA") || !strings.Contains(out, "DOWNLOAD") {
		t.Fatalf("table:\n%s", out)
	}
	if !strings.Contains(errOut, "✓ Cloudflare") {
		t.Fatalf("stderr: %q", errOut)
	}
}

func TestFailedProviderExitsOne(t *testing.T) {
	t.Setenv(cloudflare.EnvBaseURL, "http://127.0.0.1:1")
	code, out, _ := run(t, "run", "--json", "-q", "-d", "0.2", "--pings", "2")
	if code != ExitFailed {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(out, `"ok": false`) {
		t.Fatalf("stdout: %s", out)
	}
}

func TestTUIRefusesWithoutTerminal(t *testing.T) {
	code, _, errOut := run(t, "tui")
	if code != ExitUsage || !strings.Contains(errOut, "interactive terminal") {
		t.Fatalf("tui without a terminal: exit %d stderr %q", code, errOut)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"run", "-p", "nope"}, {"run", "-t", "ping"}, {"run", "--bogus"}, {"bogus"}} {
		code, _, errOut := run(t, args...)
		if code != ExitUsage || !strings.HasPrefix(errOut, "speedprobe: ") {
			t.Errorf("%v: exit %d stderr %q", args, code, errOut)
		}
	}
}

func TestRunBothProvidersAgainstTestServers(t *testing.T) {
	cf := testserver.Cloudflare()
	defer cf.Close()
	ml := testserver.MLab()
	defer ml.Close()
	t.Setenv(cloudflare.EnvBaseURL, cf.URL)
	t.Setenv(mlab.EnvLocateURL, ml.URL+"/locate")

	code, out, errOut := run(t, "run", "--all", "-d", "1", "-c", "2", "--pings", "3", "--json", "-q")
	if code != ExitOK {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var results []map[string]any
	if err := json.Unmarshal([]byte(out), &results); err != nil || len(results) != 2 {
		t.Fatalf("results: %v\n%s", err, out)
	}
	if results[0]["provider"] != "cloudflare" || results[1]["provider"] != "mlab" {
		t.Fatalf("order: %v %v", results[0]["provider"], results[1]["provider"])
	}
	if results[1]["ok"] != true || results[1]["download_mbps"].(float64) <= 0 {
		t.Fatalf("mlab result: %v", results[1])
	}
}

func TestListAndVersion(t *testing.T) {
	code, out, _ := run(t, "list")
	if code != ExitOK || !strings.Contains(out, "cloudflare") || !strings.Contains(out, "cf") {
		t.Fatalf("list: %d %q", code, out)
	}
	if !strings.Contains(out, "mlab") || !strings.Contains(out, "publishes every test") {
		t.Fatalf("list must show mlab and its data policy: %q", out)
	}
	code, out, _ = run(t, "--version")
	if code != ExitOK || strings.TrimSpace(out) != "speedprobe test" {
		t.Fatalf("version: %d %q", code, out)
	}
}
