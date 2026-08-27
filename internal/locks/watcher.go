// Package locks watches live events for betting lock/unlock transitions
// and streams them to subscribers.
//
// 1xbet's platform has no websocket push: live data is short-polled from
// the v3 feed (games1x2 + per-game gameEvents). This watcher polls at a
// conservative interval, diffs per-outcome lock state, and emits timestamped
// lock/unlock events.
package locks

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"

	"xbet-api/internal/model"
)

// Event is one lock/unlock transition.
type Event struct {
	At          time.Time      `json:"at"`
	Locked      bool           `json:"locked"` // true = locked, false = unlocked
	EventID     int64          `json:"event_id"`
	EventName   string         `json:"event_name"`
	SportID     int            `json:"sport_id"`
	LeagueName  string         `json:"league_name,omitempty"`
	MarketID    int64          `json:"market_id"`
	MarketName  string         `json:"market_name"`
	OutcomeID   int64          `json:"outcome_id"`
	OutcomeName string         `json:"outcome_name"`
	Odds        float64        `json:"odds,omitempty"`
	Score       map[string]int `json:"score,omitempty"`
}

// Fetcher is the live-data source (implemented by *xbet.Client).
type Fetcher interface {
	GetLiveEvents(ctx context.Context, sportID, count int) ([]model.Event, error)
	GetLiveGame(ctx context.Context, gameID int64) (model.EventDetail, error)
}

// Watcher polls live events and game markets, diffing lock state.
type Watcher struct {
	fetcher  Fetcher
	interval time.Duration

	mu        sync.Mutex
	watched   map[int64]bool                     // game ids whose full markets are polled
	state     map[int64]map[int64]map[int64]bool // event -> market -> outcome -> locked
	subs      map[chan Event]bool
	recent    []Event
	recentCap int
	lastSeen  map[int64]time.Time // event id -> last time it appeared in the feed
}

// New creates a Watcher. interval <= 0 defaults to 5s.
func New(f Fetcher, interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Watcher{
		fetcher:   f,
		interval:  interval,
		watched:   map[int64]bool{},
		state:     map[int64]map[int64]map[int64]bool{},
		subs:      map[chan Event]bool{},
		recentCap: 500,
		lastSeen:  map[int64]time.Time{},
	}
}

// Watch requests full-market polling for a game.
func (w *Watcher) Watch(gameID int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.watched[gameID] = true
}

// Unwatch stops full-market polling for a game.
func (w *Watcher) Unwatch(gameID int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.watched, gameID)
	delete(w.state, gameID)
}

// Subscribe returns a channel receiving lock events. Unsubscribe via the
// returned cancel func.
func (w *Watcher) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	w.mu.Lock()
	w.subs[ch] = true
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		if _, ok := w.subs[ch]; ok {
			delete(w.subs, ch)
			close(ch)
		}
		w.mu.Unlock()
	}
}

// Recent returns the recent lock events, newest first.
func (w *Watcher) Recent() []Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Event, len(w.recent))
	copy(out, w.recent)
	return out
}

// Start begins polling until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) {
	log.Printf("lock watcher: polling every %s", w.interval)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	w.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *Watcher) poll(ctx context.Context) {
	// 1) live events list (covers main markets + presence)
	events, err := w.fetcher.GetLiveEvents(ctx, 0, 100)
	if err != nil {
		log.Printf("lock watcher: live events poll failed: %v", err)
	} else {
		w.applyEvents(events)
	}
	// 2) full markets for watched games
	w.mu.Lock()
	watched := make([]int64, 0, len(w.watched))
	for id := range w.watched {
		watched = append(watched, id)
	}
	w.mu.Unlock()
	for _, id := range watched {
		detail, err := w.fetcher.GetLiveGame(ctx, id)
		if err != nil {
			log.Printf("lock watcher: game %d poll failed: %v", id, err)
			continue
		}
		w.applyDetail(detail)
	}
}

// applyEvents records main-market lock state for all live events.
func (w *Watcher) applyEvents(events []model.Event) {
	now := time.Now()
	w.mu.Lock()
	seen := map[int64]bool{}
	for _, e := range events {
		seen[e.ID] = true
		w.lastSeen[e.ID] = now
	}
	for id := range w.lastSeen {
		if !seen[id] && now.Sub(w.lastSeen[id]) > 2*time.Minute {
			delete(w.lastSeen, id)
			delete(w.state, id)
		}
	}
	w.mu.Unlock()

	for _, e := range events {
		if e.MainOdds == nil {
			continue
		}
		// main market id 1: outcomes 1,2,3
		applyOutcome(w, e, 1, "Match Winner", 1, "1", e.MainOdds.Home, e.Locked)
		applyOutcome(w, e, 1, "Match Winner", 2, "X", e.MainOdds.Draw, e.Locked)
		applyOutcome(w, e, 1, "Match Winner", 3, "2", e.MainOdds.Away, e.Locked)
	}
}

// applyDetail records full-market lock state for a watched game.
func (w *Watcher) applyDetail(detail model.EventDetail) {
	for _, m := range detail.Markets {
		for _, o := range m.Outcomes {
			applyOutcome(w, detail.Event, m.ID, m.Name, o.ID, o.Name, o.Odds, o.Locked)
		}
	}
}

func applyOutcome(w *Watcher, e model.Event, marketID int64, marketName string, outcomeID int64, outcomeName string, odds float64, locked bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	evState := w.state[e.ID]
	if evState == nil {
		evState = map[int64]map[int64]bool{}
		w.state[e.ID] = evState
	}
	mState := evState[marketID]
	if mState == nil {
		mState = map[int64]bool{}
		evState[marketID] = mState
	}
	prev, known := mState[outcomeID]
	if !known {
		// first observation: record the state without emitting (the SSE
		// handler replays recent history for fresh consumers)
		mState[outcomeID] = locked
		return
	}
	if prev == locked {
		return // no change
	}
	mState[outcomeID] = locked
	ev := Event{
		At:          time.Now(),
		Locked:      locked,
		EventID:     e.ID,
		EventName:   e.Home + " vs " + e.Away,
		SportID:     e.SportID,
		LeagueName:  e.LeagueName,
		MarketID:    marketID,
		MarketName:  marketName,
		OutcomeID:   outcomeID,
		OutcomeName: outcomeName,
		Odds:        odds,
		Score:       e.Score,
	}
	w.broadcastLocked(ev)
}

// broadcastLocked appends to recent and fans out to subscribers. Caller holds mu.
func (w *Watcher) broadcastLocked(ev Event) {
	w.recent = append(w.recent, ev)
	if len(w.recent) > w.recentCap {
		w.recent = w.recent[len(w.recent)-w.recentCap:]
	}
	for ch := range w.subs {
		select {
		case ch <- ev:
		default: // slow subscriber: drop rather than block
		}
	}
}

// SortRecent returns recent events sorted newest-first (helper for handlers).
func (w *Watcher) SortRecent() []Event {
	out := w.Recent()
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}
