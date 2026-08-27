package xbet

import (
	"math/rand"
	"sync"
)

// MirrorPool manages the list of 1xbet mirror hosts (e.g. "ua.1xbet.com").
// Mirrors that fail are temporarily demoted; healthy ones are preferred.
type MirrorPool struct {
	mu      sync.Mutex
	mirrors []string // all known mirrors
	healthy []bool   // parallel to mirrors
	fails   []int    // consecutive failure counts
	pref    int      // index of last known-good mirror (-1 = none yet)
}

// NewMirrorPool creates a pool from a list of hosts.
func NewMirrorPool(hosts []string) *MirrorPool {
	p := &MirrorPool{
		mirrors: hosts,
		healthy: make([]bool, len(hosts)),
		fails:   make([]int, len(hosts)),
		pref:    -1,
	}
	for i := range p.healthy {
		p.healthy[i] = true
	}
	return p
}

// Hosts returns the current preferred order: last-good mirror first,
// then the rest (healthy ones before unhealthy), randomly shuffled
// within each tier to spread load.
func (p *MirrorPool) Hosts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.mirrors) == 0 {
		return nil
	}

	order := make([]string, 0, len(p.mirrors))
	seen := map[int]bool{}

	addTier := func(cond func(i int) bool) {
		var tier []int
		for i, m := range p.mirrors {
			if seen[i] || !cond(i) {
				continue
			}
			tier = append(tier, i)
			seen[i] = true
			_ = m
		}
		rand.Shuffle(len(tier), func(a, b int) { tier[a], tier[b] = tier[b], tier[a] })
		for _, i := range tier {
			order = append(order, p.mirrors[i])
		}
	}

	if p.pref >= 0 && p.healthy[p.pref] {
		order = append(order, p.mirrors[p.pref])
		seen[p.pref] = true
	}
	addTier(func(i int) bool { return p.healthy[i] })
	addTier(func(i int) bool { return !p.healthy[i] })
	return order
}

// ReportFailure marks a host as failed and rotates the preferred mirror.
func (p *MirrorPool) ReportFailure(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, m := range p.mirrors {
		if m != host {
			continue
		}
		p.fails[i]++
		if p.fails[i] >= 2 {
			p.healthy[i] = false
		}
		if p.pref == i {
			p.pref = -1
		}
		return
	}
}

// ReportSuccess marks a host as healthy and makes it the preferred mirror.
func (p *MirrorPool) ReportSuccess(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, m := range p.mirrors {
		if m != host {
			continue
		}
		p.fails[i] = 0
		p.healthy[i] = true
		p.pref = i
		return
	}
}

// ProbeAndRestore periodically re-checks demoted mirrors so they can
// rejoin the pool when the block/outage clears. Call as a goroutine.
func (p *MirrorPool) ProbeAndRestore(check func(host string) error) {
	// snapshot demoted hosts
	p.mu.Lock()
	var demoted []string
	for i, m := range p.mirrors {
		if !p.healthy[i] {
			demoted = append(demoted, m)
		}
	}
	p.mu.Unlock()

	for _, h := range demoted {
		if err := check(h); err == nil {
			p.ReportSuccess(h)
		}
	}
}
