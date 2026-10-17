package engine

import (
	"sync"
	"time"
)

// Meter counts transferred bytes and reports a steady-state rate that skips
// the warm-up window, so TCP slow-start does not drag the average down.
type Meter struct {
	mu     sync.Mutex
	t0     time.Time
	warmup time.Duration
	total  int64
	tLast  time.Time
	warmAt time.Time // when the warm-up window ended
	warmB  int64     // bytes counted at that moment
	warmed bool
	hist   []sample // recent samples for the live rate, at most 8
}

type sample struct {
	t time.Time
	b int64
}

// NewMeter starts the clock.
func NewMeter(warmup time.Duration) *Meter {
	now := time.Now()
	return &Meter{t0: now, warmup: warmup, tLast: now}
}

// Start is when the meter was created.
func (m *Meter) Start() time.Time { return m.t0 }

// Add records n more bytes.
func (m *Meter) Add(n int) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.warmed && now.Sub(m.t0) >= m.warmup {
		m.warmed, m.warmAt, m.warmB = true, now, m.total
	}
	m.total += int64(n)
	m.tLast = now
}

// Total is the number of bytes counted so far.
func (m *Meter) Total() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.total
}

// LiveMbps is the rate over roughly the last two seconds, for progress display.
func (m *Meter) LiveMbps() float64 {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hist = append(m.hist, sample{now, m.total})
	if len(m.hist) > 8 {
		m.hist = m.hist[1:]
	}
	a, b := m.hist[0], m.hist[len(m.hist)-1]
	dt := b.t.Sub(a.t).Seconds()
	if dt <= 0 {
		return 0
	}
	return float64(b.b-a.b) * 8 / dt / 1e6
}

// Mbps is the steady-state rate: bytes after warm-up over time after warm-up.
// Before warm-up completes it falls back to the overall average.
func (m *Meter) Mbps() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.warmed && m.tLast.After(m.warmAt) {
		return float64(m.total-m.warmB) * 8 / m.tLast.Sub(m.warmAt).Seconds() / 1e6
	}
	dt := m.tLast.Sub(m.t0).Seconds()
	if dt <= 0 {
		return 0
	}
	return float64(m.total) * 8 / dt / 1e6
}
