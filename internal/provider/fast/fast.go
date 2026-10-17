// Package fast measures against Netflix's Open Connect caches the way
// fast.com's web page does. There is no public API: the page's script
// carries a token, and api.fast.com hands out target URLs for it. Netflix
// can change any of that without notice, which is why this provider is
// experimental. Upload is not offered: the page's upload endpoints are
// undocumented browser internals.
package fast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/akshtray/speedprobe/internal/engine"
)

// Environment overrides for tests and mirrors.
const (
	EnvBaseURL = "SPEEDPROBE_FAST_URL"     // the web page, default https://fast.com
	EnvAPIURL  = "SPEEDPROBE_FAST_API_URL" // the API, default https://api.fast.com/netflix/speedtest/v2
)

const (
	defaultBase   = "https://fast.com"
	defaultAPI    = "https://api.fast.com/netflix/speedtest/v2"
	downloadBytes = 26_214_400
)

var (
	scriptRe = regexp.MustCompile(`src="(/app-[^"]+\.js)"`)
	tokenRe  = regexp.MustCompile(`token:"([A-Za-z0-9]+)"`)
)

// Fast is the provider.
type Fast struct {
	base, api string
	client    *http.Client
}

// New returns a Fast.com provider using client for all requests.
func New(client *http.Client) *Fast {
	base, api := os.Getenv(EnvBaseURL), os.Getenv(EnvAPIURL)
	if base == "" {
		base = defaultBase
	}
	if api == "" {
		api = defaultAPI
	}
	return &Fast{base: strings.TrimRight(base, "/"), api: api, client: client}
}

func (f *Fast) Info() engine.Info {
	return engine.Info{
		Name:         "fast",
		Title:        "Fast.com (Netflix)",
		URL:          "https://fast.com/",
		Aliases:      []string{"fastcom", "fast.com", "netflix"},
		Kind:         engine.Native,
		Experimental: true,
		Capabilities: []engine.Measurement{engine.Latency, engine.Download},
		Blurb:        "Netflix Open Connect caches via fast.com's unofficial API; latency and download only",
	}
}

func (f *Fast) Available() (bool, string) { return true, "ready (experimental: unofficial API)" }

func (f *Fast) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := engine.CheckStatus(resp); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

type apiResponse struct {
	Client struct {
		ISP string `json:"isp"`
		ASN string `json:"asn"`
	} `json:"client"`
	Targets []struct {
		URL      string `json:"url"`
		Location struct {
			City    string `json:"city"`
			Country string `json:"country"`
		} `json:"location"`
	} `json:"targets"`
}

// targets scrapes the token from fast.com's app script and asks the API
// for Open Connect target URLs.
func (f *Fast) targets(ctx context.Context) (*apiResponse, error) {
	html, err := f.get(ctx, f.base+"/")
	if err != nil {
		return nil, err
	}
	m := scriptRe.FindSubmatch(html)
	if m == nil {
		return nil, errors.New("fast.com app script not found (page layout changed?)")
	}
	js, err := f.get(ctx, f.base+string(m[1]))
	if err != nil {
		return nil, err
	}
	tok := tokenRe.FindSubmatch(js)
	if tok == nil {
		return nil, errors.New("fast.com API token not found (app script changed?)")
	}
	q := url.Values{"https": {"true"}, "token": {string(tok[1])}, "urlCount": {"5"}}
	body, err := f.get(ctx, f.api+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var out apiResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("fast.com API: %w", err)
	}
	out.Targets = slices.DeleteFunc(out.Targets, func(t struct {
		URL      string `json:"url"`
		Location struct {
			City    string `json:"city"`
			Country string `json:"country"`
		} `json:"location"`
	}) bool {
		return t.URL == ""
	})
	if len(out.Targets) == 0 {
		return nil, errors.New("fast.com API returned no targets")
	}
	return &out, nil
}

// ranged appends /range/0-end to a target's path; the caches honour it.
func ranged(raw string, end int) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Path = strings.TrimRight(u.Path, "/") + fmt.Sprintf("/range/0-%d", end)
	return u.String()
}

func (f *Fast) Run(ctx engine.Context, opts engine.Options, sink engine.Sink) *engine.Result {
	info := f.Info()
	res := engine.NewResult(info.Name)
	sink.Emit(engine.Event{Kind: engine.Status, Provider: info.Name, Title: info.Title, Message: "fetching Open Connect targets…"})
	data, err := f.targets(ctx)
	if err != nil {
		res.Fail("targets", err)
		return res
	}
	urls := make([]string, 0, len(data.Targets))
	var locs []string
	for _, t := range data.Targets {
		urls = append(urls, t.URL)
		loc := or(t.Location.City, "?") + ", " + or(t.Location.Country, "?")
		if !slices.Contains(locs, loc) {
			locs = append(locs, loc)
		}
	}
	slices.Sort(locs)
	res.Server = strings.Join(locs, " | ")
	res.Extra["isp"] = data.Client.ISP
	res.Extra["asn"] = data.Client.ASN
	res.Extra["targets"] = len(urls)

	probe := func(ctx context.Context) (float64, error) {
		t := time.Now()
		if _, err := f.get(ctx, ranged(urls[0], 0)); err != nil {
			return 0, err
		}
		return float64(time.Since(t).Microseconds()) / 1000, nil
	}
	if opts.Wants(engine.Latency) {
		sink.Emit(engine.Event{Kind: engine.PhaseStarted, Provider: info.Name, Title: info.Title, Phase: engine.Latency})
		var err error
		res.LatencyMs, res.JitterMs, err = engine.IdleLatency(ctx, info.Name, info.Title, probe, opts.Pings, sink)
		if err != nil {
			res.Fail("latency", err)
		}
		sink.Emit(engine.Event{Kind: engine.PhaseFinished, Provider: info.Name, Title: info.Title, Phase: engine.Latency})
	}
	if opts.Wants(engine.Download) {
		sink.Emit(engine.Event{Kind: engine.PhaseStarted, Provider: info.Name, Title: info.Title, Phase: engine.Download})
		worker := engine.HTTPDownload(f.client, func(i int) string { return ranged(urls[i%len(urls)], downloadBytes) })
		mbps, loaded, err := engine.Transfer(ctx, engine.Download, info.Name, info.Title, worker, opts, sink, probe)
		if err != nil {
			res.Fail("download", err)
		} else {
			res.DownloadMbps, res.LoadedLatencyDownMs = engine.F(mbps), loaded
		}
		sink.Emit(engine.Event{Kind: engine.PhaseFinished, Provider: info.Name, Title: info.Title, Phase: engine.Download})
	}
	return res
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
