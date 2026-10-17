// Package cloudflare measures against speed.cloudflare.com using its
// __down and __up endpoints. The protocol is plain HTTP; no library needed.
package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/akshtray/speedprobe/internal/engine"
)

// EnvBaseURL overrides the endpoint, for tests and mirrors.
const EnvBaseURL = "SPEEDPROBE_CLOUDFLARE_URL"

const (
	defaultBase   = "https://speed.cloudflare.com"
	downloadBytes = 25_000_000
)

var durRe = regexp.MustCompile(`dur=([\d.]+)`)

// Cloudflare is the provider.
type Cloudflare struct {
	base   string
	client *http.Client

	mu   sync.Mutex
	colo string
}

// New returns a Cloudflare provider using client for all requests.
func New(client *http.Client) *Cloudflare {
	base := os.Getenv(EnvBaseURL)
	if base == "" {
		base = defaultBase
	}
	return &Cloudflare{base: strings.TrimRight(base, "/"), client: client}
}

func (c *Cloudflare) Info() engine.Info {
	return engine.Info{
		Name:         "cloudflare",
		Title:        "Cloudflare",
		URL:          "https://speed.cloudflare.com/",
		Aliases:      []string{"cf"},
		Kind:         engine.Native,
		Capabilities: engine.AllMeasurements,
		Blurb:        "latency, jitter, loaded latency (bufferbloat), down/up",
	}
}

func (c *Cloudflare) Available() (bool, string) { return true, "ready" }

// probe times one tiny request on a kept-alive connection, subtracting the
// server's own processing time from the Server-Timing header. The cf-ray
// header ends with the IATA code of the PoP that answered.
func (c *Cloudflare) probe(ctx context.Context) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/__down?bytes=0", nil)
	if err != nil {
		return 0, err
	}
	t := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	ms := float64(time.Since(t).Microseconds()) / 1000
	if err := engine.CheckStatus(resp); err != nil {
		return 0, err
	}
	if m := durRe.FindStringSubmatch(resp.Header.Get("Server-Timing")); m != nil {
		if dur, err := strconv.ParseFloat(m[1], 64); err == nil {
			ms = max(ms-dur, 0)
		}
	}
	if ray := resp.Header.Get("Cf-Ray"); strings.Contains(ray, "-") {
		c.setColo(ray[strings.LastIndex(ray, "-")+1:])
	}
	return ms, nil
}

func (c *Cloudflare) setColo(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.colo = s
}

func (c *Cloudflare) getColo() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.colo
}

// describe fills Extra with client/network details. /meta is the richer
// source (ASN, organisation, city) but Cloudflare answers it with 403 to
// anything that is not their web page, so /cdn-cgi/trace is the fallback:
// plain text, always available, with ip, colo and country. Failure of both
// is not an error; the measurements do not depend on it.
func (c *Cloudflare) describe(ctx context.Context, res *engine.Result) {
	if c.meta(ctx, res) {
		return
	}
	c.trace(ctx, res)
}

func (c *Cloudflare) meta(ctx context.Context, res *engine.Result) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/meta", nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return false
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return false
	}
	found := false
	for _, k := range []string{"clientIp", "asn", "asOrganization", "city", "country"} {
		if v, ok := m[k]; ok {
			res.Extra[k] = v
			found = true
		}
	}
	switch colo := m["colo"].(type) {
	case string:
		if colo != "" {
			c.setColo(colo)
		}
	case map[string]any:
		for _, k := range []string{"iata", "city"} {
			if s, ok := colo[k].(string); ok && s != "" {
				c.setColo(s)
				break
			}
		}
	}
	return found
}

// trace reads /cdn-cgi/trace, a key=value-per-line document.
func (c *Cloudflare) trace(ctx context.Context, res *engine.Result) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/cdn-cgi/trace", nil)
	if err != nil {
		return
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || v == "" {
			continue
		}
		switch k {
		case "ip":
			res.Extra["clientIp"] = v
		case "loc":
			res.Extra["country"] = v
		case "colo":
			c.setColo(v)
		}
	}
}

func (c *Cloudflare) Run(ctx engine.Context, opts engine.Options, sink engine.Sink) *engine.Result {
	info := c.Info()
	res := engine.NewResult(info.Name)
	c.describe(ctx, res)

	if opts.Wants(engine.Latency) {
		sink.Emit(engine.Event{Kind: engine.PhaseStarted, Provider: info.Name, Title: info.Title, Phase: engine.Latency})
		var err error
		res.LatencyMs, res.JitterMs, err = engine.IdleLatency(ctx, info.Name, info.Title, c.probe, opts.Pings, sink)
		if err != nil {
			res.Fail("latency", err)
		}
		sink.Emit(engine.Event{Kind: engine.PhaseFinished, Provider: info.Name, Title: info.Title, Phase: engine.Latency})
	}
	if opts.Wants(engine.Download) {
		sink.Emit(engine.Event{Kind: engine.PhaseStarted, Provider: info.Name, Title: info.Title, Phase: engine.Download})
		url := fmt.Sprintf("%s/__down?bytes=%d", c.base, downloadBytes)
		mbps, loaded, err := engine.Transfer(ctx, engine.Download, info.Name, info.Title,
			engine.HTTPDownload(c.client, func(int) string { return url }), opts, sink, c.probe)
		if err != nil {
			res.Fail("download", err)
		} else {
			res.DownloadMbps, res.LoadedLatencyDownMs = engine.F(mbps), loaded
		}
		sink.Emit(engine.Event{Kind: engine.PhaseFinished, Provider: info.Name, Title: info.Title, Phase: engine.Download})
	}
	if opts.Wants(engine.Upload) {
		sink.Emit(engine.Event{Kind: engine.PhaseStarted, Provider: info.Name, Title: info.Title, Phase: engine.Upload})
		url := c.base + "/__up"
		headers := http.Header{"Content-Type": []string{"text/plain;charset=UTF-8"}}
		mbps, loaded, err := engine.Transfer(ctx, engine.Upload, info.Name, info.Title,
			engine.HTTPUpload(c.client, func(int, int) string { return url }, engine.UploadOptions{Headers: headers}),
			opts, sink, c.probe)
		if err != nil {
			res.Fail("upload", err)
		} else {
			res.UploadMbps, res.LoadedLatencyUpMs = engine.F(mbps), loaded
		}
		sink.Emit(engine.Event{Kind: engine.PhaseFinished, Provider: info.Name, Title: info.Title, Phase: engine.Upload})
	}
	res.Server = "Cloudflare"
	if colo := c.getColo(); colo != "" {
		res.Server += " " + colo
	}
	return res
}
