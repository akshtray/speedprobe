package engine

import (
	"context"
	"strings"
	"time"
)

// RunAll runs each provider in turn and returns one Result per provider.
// Runs never overlap: concurrent transfers would share the link and both
// results would be wrong (see CONTEXT.md, Session). Events go to sink.
//
// A provider that is not available yields a failed Result without running.
// Measurements a provider lacks the capability for are not requested of it;
// they stay null and the Result's note says so (CONTEXT.md, Capability).
// Each run is bounded by twice the transfer duration plus a generous margin.
func RunAll(ctx context.Context, providers []Provider, opts Options, sink Sink) []*Result {
	results := make([]*Result, 0, len(providers))
	for _, p := range providers {
		info := p.Info()
		if ok, why := p.Available(); !ok {
			results = append(results, Failed(info.Name, why))
			continue
		}
		if ctx.Err() != nil {
			results = append(results, Failed(info.Name, Describe(ctx.Err())))
			continue
		}
		sink.Emit(Event{Kind: RunStarted, Provider: info.Name, Title: info.Title})
		runOpts, skipped := narrow(opts, info)
		var r *Result
		if len(runOpts.Measurements) > 0 {
			runCtx, cancel := context.WithTimeout(ctx, opts.Duration*2+180*time.Second)
			r = p.Run(runCtx, runOpts, sink)
			cancel()
		}
		if r == nil {
			r = NewResult(info.Name)
			if len(runOpts.Measurements) > 0 {
				r = Failed(info.Name, "provider returned no result")
			}
		}
		if len(skipped) > 0 {
			r.Note = joinNote(r.Note, strings.Join(skipped, "/")+" not supported by "+info.Title)
		}
		results = append(results, r)
		sink.Emit(Event{Kind: RunFinished, Provider: info.Name, Title: info.Title, Result: r})
	}
	return results
}

// narrow drops the measurements info cannot supply and names them.
func narrow(opts Options, info Info) (Options, []string) {
	kept := make([]Measurement, 0, len(opts.Measurements))
	var skipped []string
	for _, m := range opts.Measurements {
		if info.Supports(m) {
			kept = append(kept, m)
		} else {
			skipped = append(skipped, string(m))
		}
	}
	opts.Measurements = kept
	return opts, skipped
}

func joinNote(existing, extra string) string {
	if existing == "" {
		return extra
	}
	return existing + "; " + extra
}
