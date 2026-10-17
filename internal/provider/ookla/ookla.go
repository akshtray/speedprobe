// Package ookla is a wrapped provider: it runs Ookla's official `speedtest`
// CLI and parses its JSON. Ookla publishes no protocol, and their CLI page
// tells third parties to wrap the binary, so this is the sanctioned route.
// speedprobe never downloads or redistributes the binary.
package ookla

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akshtray/speedprobe/internal/engine"
)

// EnvBinary overrides where the speedtest binary is looked for (tests).
const EnvBinary = "SPEEDPROBE_OOKLA_BIN"

const (
	installHint   = "install Ookla's speedtest CLI: https://www.speedtest.net/apps/cli"
	installDetail = installHint + " (standalone tarball: https://install.speedtest.net/app/cli/ ; " +
		"the apt repository does not serve Ubuntu 24.04, so use the tarball there)"
	runTimeout = 180 * time.Second
)

// Config holds the user's provider-specific choices.
type Config struct {
	ServerID      int  // pin an Ookla server (see `speedtest -L`)
	AcceptLicense bool // pass --accept-license --accept-gdpr
}

// Ookla is the provider.
type Ookla struct {
	cfg    Config
	once   sync.Once
	exe    string
	flavor string // "ookla", "speedtest-cli" (the abandoned python tool) or ""
}

// New returns the provider; the binary is looked up lazily.
func New(cfg Config) *Ookla { return &Ookla{cfg: cfg} }

func (o *Ookla) Info() engine.Info {
	return engine.Info{
		Name:         "ookla",
		Title:        "Ookla Speedtest",
		URL:          "https://www.speedtest.net/",
		Aliases:      []string{"speedtest", "speedtest.net"},
		Kind:         engine.Wrapped,
		Capabilities: engine.AllMeasurements,
		Blurb:        "wraps Ookla's official `speedtest` CLI; it always runs every measurement",
	}
}

func (o *Ookla) detect() (string, string) {
	o.once.Do(func() {
		exe := os.Getenv(EnvBinary)
		if exe != "" {
			if _, err := os.Stat(exe); err != nil {
				return
			}
		} else {
			var err error
			if exe, err = exec.LookPath("speedtest"); err != nil {
				return
			}
		}
		o.exe, o.flavor = exe, "ookla"
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, _ := exec.CommandContext(ctx, exe, "--version").CombinedOutput()
		if strings.Contains(string(out), "speedtest-cli") {
			o.flavor = "speedtest-cli"
		}
	})
	return o.exe, o.flavor
}

func (o *Ookla) Available() (bool, string) {
	exe, flavor := o.detect()
	switch {
	case exe == "":
		return false, "not installed; " + installHint
	case flavor == "speedtest-cli":
		return false, "found the unmaintained python speedtest-cli, not Ookla's CLI; " + installHint
	default:
		return true, "ookla (" + exe + ")"
	}
}

// resultJSON is the subset of the CLI's `type: result` object we read.
type resultJSON struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Ping    struct {
		Latency *float64 `json:"latency"`
		Jitter  *float64 `json:"jitter"`
	} `json:"ping"`
	Download   transfer `json:"download"`
	Upload     transfer `json:"upload"`
	PacketLoss any      `json:"packetLoss"`
	ISP        string   `json:"isp"`
	Server     struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Location string `json:"location"`
	} `json:"server"`
	Result struct {
		URL string `json:"url"`
	} `json:"result"`
}

type transfer struct {
	Bandwidth *float64 `json:"bandwidth"` // bytes per second
	Latency   *struct {
		IQM *float64 `json:"iqm"`
	} `json:"latency"`
}

func (t transfer) mbps() *float64 {
	if t.Bandwidth == nil || *t.Bandwidth == 0 {
		return nil
	}
	return engine.F(*t.Bandwidth * 8 / 1e6)
}

func (t transfer) loaded() *float64 {
	if t.Latency == nil {
		return nil
	}
	return t.Latency.IQM
}

func (o *Ookla) Run(ctx engine.Context, opts engine.Options, sink engine.Sink) *engine.Result {
	info := o.Info()
	res := engine.NewResult(info.Name)
	exe, flavor := o.detect()
	if exe == "" || flavor != "ookla" {
		res.Fail("run", errors.New(installDetail))
		return res
	}
	args := []string{"--format=json", "--progress=no"}
	if o.cfg.AcceptLicense {
		args = append(args, "--accept-license", "--accept-gdpr")
	}
	if o.cfg.ServerID != 0 {
		args = append(args, "--server-id", strconv.Itoa(o.cfg.ServerID))
	}
	if len(opts.Measurements) != len(engine.AllMeasurements) {
		res.Note = "Ookla CLI always runs every measurement"
	}
	sink.Emit(engine.Event{Kind: engine.Status, Provider: info.Name, Title: info.Title, Message: "running speedtest (about 30 s)…"})

	runCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, exe, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()

	var result *resultJSON
	var msgs []string
	for line := range strings.SplitSeq(stdout.String()+"\n"+stderr.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj resultJSON
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			msgs = append(msgs, line)
			continue
		}
		switch {
		case obj.Type == "result":
			r := obj
			result = &r
		case obj.Message != "":
			msgs = append(msgs, obj.Message)
		}
	}
	if result == nil {
		res.Fail("run", o.explain(runCtx, runErr, msgs))
		return res
	}

	res.Server = fmt.Sprintf("%s (%s) #%d", or(result.Server.Name, "?"), or(result.Server.Location, "?"), result.Server.ID)
	res.LatencyMs, res.JitterMs = result.Ping.Latency, result.Ping.Jitter
	res.DownloadMbps, res.LoadedLatencyDownMs = result.Download.mbps(), result.Download.loaded()
	res.UploadMbps, res.LoadedLatencyUpMs = result.Upload.mbps(), result.Upload.loaded()
	if pl, ok := result.PacketLoss.(float64); ok {
		res.PacketLossPct = engine.F(pl)
	}
	if result.ISP != "" {
		res.Extra["isp"] = result.ISP
	}
	if result.Result.URL != "" {
		res.Extra["result_url"] = result.Result.URL
	}
	return res
}

// explain turns a run without a result into the most useful one-line error.
func (o *Ookla) explain(ctx context.Context, runErr error, msgs []string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("speedtest binary timed out after %s", runTimeout)
	}
	blob := strings.ToLower(strings.Join(msgs, " "))
	if !o.cfg.AcceptLicense && (strings.Contains(blob, "license") || strings.Contains(blob, "gdpr")) {
		return errors.New("Ookla license not accepted: run `speedtest` once interactively, or pass --accept-ookla-license")
	}
	if len(msgs) > 0 {
		return errors.New(msgs[len(msgs)-1])
	}
	if runErr != nil {
		return runErr
	}
	return errors.New("speedtest produced no result")
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
