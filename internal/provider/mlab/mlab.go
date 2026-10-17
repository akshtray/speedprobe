// Package mlab measures against Measurement Lab's ndt7 servers using M-Lab's
// official Go client. ndt7 has no standalone ping: latency comes from the
// server's TCP_INFO during a transfer, so a latency-only run still downloads.
//
// M-Lab publishes every test, including the client IP, as open data. That is
// stated in Info.DataPolicy and shown by `speedprobe list`.
package mlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	v2 "github.com/m-lab/locate/api/v2"
	ndt7 "github.com/m-lab/ndt7-client-go"
	"github.com/m-lab/ndt7-client-go/spec"

	"github.com/akshtray/speedprobe/internal/engine"
)

// EnvLocateURL overrides the Locate API endpoint, for tests and mirrors.
const EnvLocateURL = "SPEEDPROBE_MLAB_LOCATE_URL"

const (
	defaultLocate = "https://locate.measurementlab.net/v2/nearest/ndt/ndt7"
	downloadPath  = "/ndt/v7/download"
	uploadPath    = "/ndt/v7/upload"
	clientName    = "speedprobe"
)

// Config holds the user's provider-specific choices.
type Config struct {
	ServerIndex int // index into the Locate API's nearest-first list; 0 = closest
}

// MLab is the provider.
type MLab struct {
	locateURL string
	insecure  bool // locate is plain http, so ndt7 is plain ws (tests)
	http      *http.Client
	version   string
	cfg       Config
}

// New returns an M-Lab provider. httpClient is used for the Locate API only;
// ndt7 itself runs over WebSocket through M-Lab's client library.
func New(httpClient *http.Client, version string, cfg Config) *MLab {
	locate := os.Getenv(EnvLocateURL)
	if locate == "" {
		locate = defaultLocate
	}
	return &MLab{
		locateURL: locate,
		insecure:  strings.HasPrefix(locate, "http://"),
		http:      httpClient,
		version:   version,
		cfg:       cfg,
	}
}

func (m *MLab) Info() engine.Info {
	return engine.Info{
		Name:         "mlab",
		Title:        "M-Lab (ndt7)",
		URL:          "https://www.measurementlab.net/",
		Aliases:      []string{"ndt", "ndt7", "measurementlab", "m-lab"},
		Kind:         engine.Native,
		Capabilities: engine.AllMeasurements,
		Blurb:        "research-grade ndt7; ~10s per direction fixed by protocol, TCP_INFO RTTs",
		DataPolicy:   "M-Lab publishes every test, including your IP address, as open data (https://www.measurementlab.net/privacy/)",
	}
}

func (m *MLab) Available() (bool, string) { return true, "ready" }

// locate asks the Locate API for nearby servers, nearest first.
func (m *MLab) locate(ctx context.Context) ([]v2.Target, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.locateURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := engine.CheckStatus(resp); err != nil {
		return nil, err
	}
	var body struct {
		Results []v2.Target `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("locate API: %w", err)
	}
	if len(body.Results) == 0 {
		return nil, errors.New("locate API returned no servers")
	}
	return body.Results, nil
}

// serviceURL picks the URL for path from a target, preferring wss unless
// the provider is configured insecure, and falling back to whatever exists.
func (m *MLab) serviceURL(t *v2.Target, path string) string {
	keys := []string{"wss://" + path, "ws://" + path}
	if m.insecure {
		keys[0], keys[1] = keys[1], keys[0]
	}
	for _, k := range keys {
		if u := t.URLs[k]; u != "" {
			return u
		}
	}
	return ""
}

func describe(t *v2.Target) string {
	city, country := "?", "?"
	if t.Location != nil {
		if t.Location.City != "" {
			city = t.Location.City
		}
		if t.Location.Country != "" {
			country = t.Location.Country
		}
	}
	machine := t.Machine
	if machine == "" {
		machine = "?"
	}
	return fmt.Sprintf("%s (%s, %s)", machine, city, country)
}

// stats is what one ndt7 direction yields.
type stats struct {
	clientBytes, clientElapsed int64 // bytes and µs as seen by the client
	serverBytes, serverElapsed int64 // bytes and µs as seen by the server
	minRTT, rtt, rttVar        []float64
	uuid                       string
}

// mbps is the throughput: client-side for download, server-side for upload
// (the client only knows what it buffered, not what was delivered).
func (s *stats) mbps(kind spec.TestKind) (float64, bool) {
	if kind == spec.TestUpload && s.serverElapsed > 0 && s.serverBytes > 0 {
		return float64(s.serverBytes) * 8 / float64(s.serverElapsed), true // bits per µs == Mbps
	}
	if s.clientElapsed > 0 && s.clientBytes > 0 {
		return float64(s.clientBytes) * 8 / float64(s.clientElapsed), true
	}
	return 0, false
}

// direction runs one ndt7 test, trying each target in turn until one connects.
func (m *MLab) direction(ctx context.Context, kind spec.TestKind, targets []v2.Target, info engine.Info, sink engine.Sink) (*stats, *v2.Target, error) {
	path, phase := downloadPath, engine.Download
	if kind == spec.TestUpload {
		path, phase = uploadPath, engine.Upload
	}
	var lastErr error
	for i := range targets {
		t := &targets[i]
		raw := m.serviceURL(t, path)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil {
			lastErr = err
			continue
		}
		client := ndt7.NewClient(clientName, m.version)
		client.Dialer.Proxy = http.ProxyFromEnvironment
		client.ServiceURL = u
		var ch <-chan spec.Measurement
		if kind == spec.TestDownload {
			ch, err = client.StartDownload(ctx)
		} else {
			ch, err = client.StartUpload(ctx)
		}
		if err != nil {
			lastErr = err
			continue
		}
		st := &stats{}
		for meas := range ch {
			switch meas.Origin {
			case spec.OriginClient:
				if meas.AppInfo != nil {
					st.clientBytes, st.clientElapsed = meas.AppInfo.NumBytes, meas.AppInfo.ElapsedTime
				}
			case spec.OriginServer:
				if meas.ConnectionInfo != nil && meas.ConnectionInfo.UUID != "" {
					st.uuid = meas.ConnectionInfo.UUID
				}
				if meas.AppInfo != nil {
					st.serverBytes, st.serverElapsed = meas.AppInfo.NumBytes, meas.AppInfo.ElapsedTime
				}
				if tcp := meas.TCPInfo; tcp != nil {
					if tcp.MinRTT > 0 {
						st.minRTT = append(st.minRTT, float64(tcp.MinRTT)/1000)
					}
					if tcp.RTT > 0 {
						st.rtt = append(st.rtt, float64(tcp.RTT)/1000)
					}
					if tcp.RTTVar > 0 {
						st.rttVar = append(st.rttVar, float64(tcp.RTTVar)/1000)
					}
				}
			}
			if rate, ok := st.mbps(kind); ok {
				sink.Emit(engine.Event{Kind: engine.ThroughputSample, Provider: info.Name, Title: info.Title, Phase: phase, Mbps: rate})
			}
		}
		if _, ok := st.mbps(kind); !ok {
			if kind == spec.TestDownload {
				return nil, t, errors.New("server sent no data")
			}
			return nil, t, errors.New("server received no data")
		}
		return st, t, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no usable ndt7 URLs in locate response")
	}
	return nil, nil, lastErr
}

func (m *MLab) Run(ctx engine.Context, opts engine.Options, sink engine.Sink) *engine.Result {
	info := m.Info()
	res := engine.NewResult(info.Name)

	sink.Emit(engine.Event{Kind: engine.Status, Provider: info.Name, Title: info.Title, Message: "asking the Locate API for the nearest server…"})
	targets, err := m.locate(ctx)
	if err != nil {
		res.Fail("locate", err)
		return res
	}
	machines := make([]string, 0, len(targets))
	for _, t := range targets {
		machines = append(machines, t.Machine)
	}
	res.Extra["candidates"] = machines
	candidates := targets[min(max(m.cfg.ServerIndex, 0), len(targets)-1):]

	var down, up *stats
	if opts.Wants(engine.Download) || opts.Wants(engine.Latency) {
		sink.Emit(engine.Event{Kind: engine.PhaseStarted, Provider: info.Name, Title: info.Title, Phase: engine.Download})
		st, t, err := m.direction(ctx, spec.TestDownload, candidates, info, sink)
		if t != nil {
			res.Server = describe(t)
		}
		if err != nil {
			res.Fail("download", err)
		} else {
			down = st
			if opts.Wants(engine.Download) {
				rate, _ := st.mbps(spec.TestDownload)
				res.DownloadMbps = engine.F(rate)
				res.LoadedLatencyDownMs = engine.Median(st.rtt)
			}
		}
		sink.Emit(engine.Event{Kind: engine.PhaseFinished, Provider: info.Name, Title: info.Title, Phase: engine.Download})
	}
	if opts.Wants(engine.Upload) {
		sink.Emit(engine.Event{Kind: engine.PhaseStarted, Provider: info.Name, Title: info.Title, Phase: engine.Upload})
		st, t, err := m.direction(ctx, spec.TestUpload, candidates, info, sink)
		if t != nil && res.Server == "" {
			res.Server = describe(t)
		}
		if err != nil {
			res.Fail("upload", err)
		} else {
			up = st
			rate, _ := st.mbps(spec.TestUpload)
			res.UploadMbps = engine.F(rate)
			res.LoadedLatencyUpMs = engine.Median(st.rtt)
		}
		sink.Emit(engine.Event{Kind: engine.PhaseFinished, Provider: info.Name, Title: info.Title, Phase: engine.Upload})
	}

	var mins, vars []float64
	for _, st := range []*stats{down, up} {
		if st == nil {
			continue
		}
		mins = append(mins, st.minRTT...)
		vars = append(vars, st.rttVar...)
		if st.uuid != "" {
			res.Extra["uuid"] = st.uuid
		}
	}
	if opts.Wants(engine.Latency) && len(mins) > 0 {
		best := mins[0]
		for _, v := range mins[1:] {
			best = min(best, v)
		}
		res.LatencyMs = engine.F(best)
		res.JitterMs = engine.Median(vars)
	}
	return res
}
