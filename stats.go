package main

import (
	"errors"
	"math"
	"sort"
	"time"
)

type target int

const (
	targetRouter target = iota
	targetInternet
	targetDNS
	targetWeb
	numTargets
)

var targetNames = [numTargets]string{"Router", "Internet", "DNS", "Web"}

const (
	// maxSamples is one minute of history. The heatmap shows at most this
	// many cells and p50, jitter and loss cover exactly what it shows.
	maxSamples = 60
	recentLoss = 15 // any loss this recent marks a node degraded
)

type sample struct {
	at      time.Time
	rtt     time.Duration
	err     error
	done    bool
	skipped bool // probe not applicable, e.g. no gateway on a VPN route
}

func (s sample) ok() bool   { return s.done && !s.skipped && s.err == nil }
func (s sample) lost() bool { return s.done && !s.skipped && s.err != nil }

// series holds one target's history plus session totals for the exit summary.
type series struct {
	samples []sample // oldest first
	first   int      // sequence number of samples[0]

	sent, lost  int
	sessionRTTs []float64 // ms
	jitterSum   float64
	jitterN     int
	lastOK      float64
	haveLastOK  bool
	outages     int
	outageTotal time.Duration

	failRun   int
	failStart time.Time
}

// add records a probe being sent and returns its sequence number.
func (s *series) add(at time.Time) int {
	s.samples = append(s.samples, sample{at: at})
	if len(s.samples) > maxSamples {
		drop := len(s.samples) - maxSamples
		s.samples = s.samples[drop:]
		s.first += drop
	}
	return s.first + len(s.samples) - 1
}

// outageEdge reports what a completed probe did to the outage state.
type outageEdge int

const (
	edgeNone outageEdge = iota
	edgeStarted
	edgeEnded
)

// minOutage is how many consecutive lost probes count as an outage rather
// than ordinary packet loss.
const minOutage = 2

func (s *series) complete(seq int, rtt time.Duration, err error) (outageEdge, time.Duration) {
	i := seq - s.first
	if i < 0 || i >= len(s.samples) {
		return edgeNone, 0
	}
	sm := &s.samples[i]
	sm.done = true
	if errors.Is(err, errNoRoute) {
		sm.skipped = true
		return edgeNone, 0
	}
	sm.rtt, sm.err = rtt, err
	s.sent++

	if err != nil {
		s.lost++
		s.failRun++
		if s.failRun == 1 {
			s.failStart = sm.at
		}
		if s.failRun == minOutage {
			return edgeStarted, 0
		}
		return edgeNone, 0
	}

	ms := float64(rtt) / float64(time.Millisecond)
	s.sessionRTTs = append(s.sessionRTTs, ms)
	if s.haveLastOK {
		s.jitterSum += math.Abs(ms - s.lastOK)
		s.jitterN++
	}
	s.lastOK, s.haveLastOK = ms, true

	edge, dur := edgeNone, time.Duration(0)
	if s.failRun >= minOutage {
		dur = sm.at.Sub(s.failStart)
		s.outages++
		s.outageTotal += dur
		edge = edgeEnded
	}
	s.failRun = 0
	return edge, dur
}

// completed returns up to n of the most recent finished, applicable samples.
func (s *series) completed(n int) []sample {
	var out []sample
	for i := len(s.samples) - 1; i >= 0 && len(out) < n; i-- {
		if sm := s.samples[i]; sm.done && !sm.skipped {
			out = append(out, sm)
		}
	}
	// Restore chronological order.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

type windowStats struct {
	n           int
	p50, jitter float64 // ms
	loss        float64 // fraction
	base        float64 // fastest recent RTT, used to colour the heatmap
}

// window summarises the last n seconds, which matches the visible heatmap.
func (s *series) window(n int) windowStats {
	w := s.completed(n)
	st := windowStats{n: len(w)}
	if st.n == 0 {
		return st
	}
	var rtts []float64
	lost := 0
	prev, havePrev := 0.0, false
	var jsum float64
	jn := 0
	for _, sm := range w {
		if sm.err != nil {
			lost++
			continue
		}
		ms := float64(sm.rtt) / float64(time.Millisecond)
		rtts = append(rtts, ms)
		if havePrev {
			jsum += math.Abs(ms - prev)
			jn++
		}
		prev, havePrev = ms, true
	}
	st.loss = float64(lost) / float64(st.n)
	if len(rtts) > 0 {
		st.p50 = median(rtts)
		st.base = minOf(rtts)
	}
	if jn > 0 {
		st.jitter = jsum / float64(jn)
	}
	return st
}

type nodeState int

const (
	statePending nodeState = iota
	stateOK
	stateDegraded
	stateDown
)

// jitterLimit is roughly where real-time audio and video start to suffer.
const jitterLimit = 30.0

func (s *series) state(t target, window int) nodeState {
	recent := s.completed(recentLoss)
	n := len(recent)
	if n == 0 {
		return statePending
	}
	tail := min(3, n)
	if n >= 2 {
		allLost := true
		for _, sm := range recent[n-tail:] {
			if !sm.lost() {
				allLost = false
				break
			}
		}
		if allLost {
			return stateDown
		}
	}
	for _, sm := range recent {
		if sm.lost() {
			return stateDegraded
		}
	}
	w := s.window(window)
	if w.loss >= 0.02 || (t == targetInternet && w.jitter > jitterLimit) {
		return stateDegraded
	}
	return stateOK
}

func median(xs []float64) float64 {
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	m := len(c) / 2
	if len(c)%2 == 0 {
		return (c[m-1] + c[m]) / 2
	}
	return c[m]
}

// percentile uses the nearest-rank method: the smallest value with at least
// p% of samples at or below it.
func percentile(xs []float64, p float64) float64 {
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	i := int(math.Ceil(p/100*float64(len(c)))) - 1
	return c[max(0, min(i, len(c)-1))]
}

func minOf(xs []float64) float64 {
	m := xs[0]
	for _, x := range xs[1:] {
		m = math.Min(m, x)
	}
	return m
}
