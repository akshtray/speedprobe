package engine

import (
	"context"
	"sync"
	"time"
)

// Worker moves bytes in one direction on one connection until deadline,
// counting them on the meter. Index i identifies the stream.
type Worker func(ctx context.Context, i int, m *Meter, deadline time.Time) error

// Probe measures one round trip and returns it in milliseconds.
type Probe func(ctx context.Context) (float64, error)

const tick = 250 * time.Millisecond

// Transfer runs opts.Connections workers for opts.Duration and returns the
// steady-state rate in Mbps plus the median loaded latency, if a probe was
// given. Progress goes to sink as ThroughputSample events for phase.
//
// Workers are given the deadline, not a cancelled context, so an upload in
// flight at the deadline finishes and is counted, as in the prototype.
// An error is returned only if nothing at all was transferred.
func Transfer(ctx context.Context, phase Measurement, provider, title string, w Worker, opts Options, sink Sink, probe Probe) (float64, *float64, error) {
	meter := NewMeter(opts.Warmup)
	deadline := meter.Start().Add(opts.Duration)

	side, stopSide := context.WithCancel(ctx)
	var sideWG sync.WaitGroup
	var loaded []float64

	sideWG.Go(func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-side.Done():
				return
			case <-t.C:
				sink.Emit(Event{Kind: ThroughputSample, Provider: provider, Title: title, Phase: phase, Mbps: meter.LiveMbps()})
			}
		}
	})
	if probe != nil {
		sideWG.Go(func() {
			for {
				if ms, err := probe(side); err == nil {
					loaded = append(loaded, ms)
				}
				select {
				case <-side.Done():
					return
				case <-time.After(tick):
				}
			}
		})
	}

	n := max(opts.Connections, 1)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { errs[i] = w(ctx, i, meter, deadline) })
	}
	wg.Wait()
	stopSide()
	sideWG.Wait()

	if meter.Total() == 0 {
		for _, err := range errs {
			if err != nil {
				return 0, nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
	}
	return meter.Mbps(), Median(loaded), nil
}

// IdleLatency takes n sequential samples with probe after one warm-up call
// (which absorbs DNS, TCP and TLS setup) and returns the median and jitter.
func IdleLatency(ctx context.Context, provider, title string, probe Probe, n int, sink Sink) (*float64, *float64, error) {
	if _, err := probe(ctx); err != nil {
		return nil, nil, err
	}
	n = max(n, 2)
	xs := make([]float64, 0, n)
	for i := range n {
		ms, err := probe(ctx)
		if err != nil {
			return nil, nil, err
		}
		xs = append(xs, ms)
		sink.Emit(Event{Kind: LatencySample, Provider: provider, Title: title, Phase: Latency, Index: i + 1, Total: n, Ms: ms})
	}
	return Median(xs), Jitter(xs), nil
}
