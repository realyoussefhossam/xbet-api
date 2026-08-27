package locks

import (
	"context"
	"sync"
	"testing"
	"time"

	"xbet-api/internal/model"
)

// fakeFetcher returns scripted live data; lock states flip per call.
type fakeFetcher struct {
	mu        sync.Mutex
	call      int
	lockedSeq []bool // outcome lock state per poll call
}

func (f *fakeFetcher) GetLiveEvents(_ context.Context, _, _ int) ([]model.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	locked := false
	if f.call < len(f.lockedSeq) {
		locked = f.lockedSeq[f.call]
	}
	f.call++
	odds := &model.Odds{Home: 1.5, Draw: 3.0, Away: 5.0}
	if locked {
		odds = &model.Odds{Home: 1.01, Draw: 1.01, Away: 1.01}
	}
	return []model.Event{{
		ID: 111, SportID: 1, LeagueName: "Test League",
		Home: "Home", Away: "Away", Status: model.StatusLive,
		Score:    map[string]int{"home": 1, "away": 0},
		MainOdds: odds,
		Locked:   locked,
	}}, nil
}

func (f *fakeFetcher) GetLiveGame(_ context.Context, _ int64) (model.EventDetail, error) {
	return model.EventDetail{}, nil
}

func TestWatcherEmitsTransitions(t *testing.T) {
	f := &fakeFetcher{lockedSeq: []bool{false, false, true, true, false}}
	w := New(f, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	ch, unsub := w.Subscribe()
	defer unsub()

	// each transition emits one event per main-market outcome (1/X/2);
	// assert we see a lock then an unlock for outcome 1
	var lockAt, unlockAt time.Time
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.OutcomeID != 1 {
				continue
			}
			if ev.Locked && lockAt.IsZero() {
				lockAt = ev.At
			}
			if !ev.Locked && !lockAt.IsZero() && unlockAt.IsZero() {
				unlockAt = ev.At
			}
			if !lockAt.IsZero() && !unlockAt.IsZero() {
				if !unlockAt.After(lockAt) {
					t.Fatal("unlock timestamp must be after lock timestamp")
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out: lockAt=%v unlockAt=%v", lockAt, unlockAt)
		}
	}
}

func TestWatcherRecentBuffer(t *testing.T) {
	f := &fakeFetcher{lockedSeq: []bool{false, true, false}}
	w := New(f, 15*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(w.Recent()) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	recent := w.SortRecent()
	if len(recent) < 2 {
		t.Fatalf("want >=2 recent events, got %d", len(recent))
	}
	// newest first
	if recent[0].At.Before(recent[1].At) {
		t.Fatal("recent must be newest-first")
	}
}
