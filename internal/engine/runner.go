package engine

import (
	"context"
	"time"
)

// RunAll runs each provider in turn and returns one Result per provider.
// Runs never overlap: concurrent transfers would share the link and both
// results would be wrong (see CONTEXT.md, Session). Events go to sink.
//
// A provider that is not available yields a failed Result without running.
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
		runCtx, cancel := context.WithTimeout(ctx, opts.Duration*2+180*time.Second)
		r := p.Run(runCtx, opts, sink)
		cancel()
		if r == nil {
			r = Failed(info.Name, "provider returned no result")
		}
		results = append(results, r)
		sink.Emit(Event{Kind: RunFinished, Provider: info.Name, Title: info.Title, Result: r})
	}
	return results
}
