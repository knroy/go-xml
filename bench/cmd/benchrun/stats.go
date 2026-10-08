package main

import (
	"math"
	"sort"
)

// Stats summarises a set of timings in nanoseconds.
type Stats struct {
	Median, P25, P75, Min int64
}

// summarise returns the median, quartiles and minimum of ns, using linear
// interpolation between closest ranks. ns is not modified.
func summarise(ns []int64) Stats {
	if len(ns) == 0 {
		return Stats{}
	}
	s := append([]int64(nil), ns...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return Stats{Median: percentile(s, 50), P25: percentile(s, 25), P75: percentile(s, 75), Min: s[0]}
}

// percentile reads p (0..100) from sorted values.
func percentile(sorted []int64, p float64) int64 {
	pos := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	frac := pos - float64(lo)
	return int64(math.Round(float64(sorted[lo]) + frac*float64(sorted[hi]-sorted[lo])))
}
