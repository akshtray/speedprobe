package engine

import (
	"math"
	"slices"
)

// Median returns the median of xs, or nil for an empty slice.
func Median(xs []float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	n := len(s)
	var m float64
	if n%2 == 1 {
		m = s[n/2]
	} else {
		m = (s[n/2-1] + s[n/2]) / 2
	}
	return &m
}

// Jitter is the mean absolute difference between consecutive samples
// (RFC 3550 style), or nil with fewer than two samples.
func Jitter(xs []float64) *float64 {
	if len(xs) < 2 {
		return nil
	}
	var sum float64
	for i := 1; i < len(xs); i++ {
		sum += math.Abs(xs[i] - xs[i-1])
	}
	j := sum / float64(len(xs)-1)
	return &j
}

// F returns a pointer to v, for filling optional Result fields.
func F(v float64) *float64 { return &v }
