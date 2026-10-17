package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"time"
)

// Schema is the version of the JSON result shape. See ADR 0002.
const Schema = 1

// Result is the outcome of one run. Optional measurements are nil when the
// provider did not or could not supply them.
type Result struct {
	Provider            string
	OK                  bool
	Server              string
	LatencyMs           *float64
	JitterMs            *float64
	LoadedLatencyDownMs *float64
	LoadedLatencyUpMs   *float64
	DownloadMbps        *float64
	UploadMbps          *float64
	PacketLossPct       *float64
	Note                string
	Error               string
	Extra               map[string]any
	Timestamp           time.Time

	failures []failure // kept out of JSON; collapses identical errors across phases
}

type failure struct {
	msg    string
	phases []string
}

// NewResult starts a successful, empty result for provider.
func NewResult(provider string) *Result {
	return &Result{Provider: provider, OK: true, Extra: map[string]any{}, Timestamp: time.Now().UTC()}
}

// Failed returns a result that never ran, with the given error.
func Failed(provider, errMsg string) *Result {
	r := NewResult(provider)
	r.OK = false
	r.Error = errMsg
	return r
}

// Fail records that phase failed with err. Identical errors across phases
// collapse into one entry, e.g. "download/upload: timed out; latency: HTTP 503".
func (r *Result) Fail(phase string, err error) {
	r.OK = false
	msg := Describe(err)
	for i := range r.failures {
		if r.failures[i].msg == msg {
			r.failures[i].phases = append(r.failures[i].phases, phase)
			r.Error = r.errorText()
			return
		}
	}
	r.failures = append(r.failures, failure{msg: msg, phases: []string{phase}})
	r.Error = r.errorText()
}

func (r *Result) errorText() string {
	parts := make([]string, 0, len(r.failures))
	for _, f := range r.failures {
		parts = append(parts, strings.Join(f.phases, "/")+": "+f.msg)
	}
	return strings.Join(parts, "; ")
}

// HTTPError is a non-2xx response.
type HTTPError struct {
	Status string // e.g. "503 Service Unavailable"
	Code   int
}

func (e *HTTPError) Error() string { return "HTTP " + e.Status }

// Describe turns an error into the short text users and JSON consumers see.
func Describe(err error) string {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timed out"
	}
	s := strings.TrimSpace(err.Error())
	if s == "" {
		return fmt.Sprintf("%T", err)
	}
	return s
}

// resultJSON is the wire shape. Field names and order are a contract (ADR 0002).
type resultJSON struct {
	Provider            string         `json:"provider"`
	OK                  bool           `json:"ok"`
	Server              *string        `json:"server"`
	LatencyMs           *float64       `json:"latency_ms"`
	JitterMs            *float64       `json:"jitter_ms"`
	LoadedLatencyDownMs *float64       `json:"loaded_latency_down_ms"`
	LoadedLatencyUpMs   *float64       `json:"loaded_latency_up_ms"`
	DownloadMbps        *float64       `json:"download_mbps"`
	UploadMbps          *float64       `json:"upload_mbps"`
	PacketLossPct       *float64       `json:"packet_loss_pct"`
	Note                *string        `json:"note"`
	Error               *string        `json:"error"`
	Extra               map[string]any `json:"extra"`
	Timestamp           string         `json:"timestamp"`
	Schema              int            `json:"schema"`
}

// MarshalJSON emits the frozen schema: every field present, floats rounded to
// two decimals, absent values as null, timestamp as RFC 3339 in UTC.
func (r *Result) MarshalJSON() ([]byte, error) {
	extra := r.Extra
	if extra == nil {
		extra = map[string]any{}
	}
	return json.Marshal(resultJSON{
		Provider:            r.Provider,
		OK:                  r.OK,
		Server:              optStr(r.Server),
		LatencyMs:           round2(r.LatencyMs),
		JitterMs:            round2(r.JitterMs),
		LoadedLatencyDownMs: round2(r.LoadedLatencyDownMs),
		LoadedLatencyUpMs:   round2(r.LoadedLatencyUpMs),
		DownloadMbps:        round2(r.DownloadMbps),
		UploadMbps:          round2(r.UploadMbps),
		PacketLossPct:       round2(r.PacketLossPct),
		Note:                optStr(r.Note),
		Error:               optStr(r.Error),
		Extra:               extra,
		Timestamp:           r.Timestamp.UTC().Format("2006-01-02T15:04:05-07:00"),
		Schema:              Schema,
	})
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func round2(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := math.Round(*p*100) / 100
	return &v
}
