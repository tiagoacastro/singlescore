package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

// Service.shouldRefresh is the gate the per-match live-focus poll
// depends on. If this dedupe window regresses, concurrent viewers each
// fire an upstream API call every 12 s and the free-tier quota burns up
// almost instantly. Keep this test tight.

func TestShouldRefresh_LiveMatchDedupedWithinWindow(t *testing.T) {
	s := New(nil, nil, nil, nil)
	m := repository.Match{ID: "m1", Status: repository.StatusLive}

	if !s.shouldRefresh(m) {
		t.Fatal("first call for a LIVE match must trigger a refresh")
	}
	if s.shouldRefresh(m) {
		t.Fatal("second call within window must NOT trigger a refresh")
	}
	// Simulate the window elapsing by rewinding the stored timestamp.
	s.refreshMu.Lock()
	s.lastRefreshedAt[m.ID] = time.Now().Add(-RefreshWindow - time.Second)
	s.refreshMu.Unlock()
	if !s.shouldRefresh(m) {
		t.Fatal("after window elapses, next call must trigger a refresh")
	}
}

func TestShouldRefresh_RejectsFarFutureScheduled(t *testing.T) {
	s := New(nil, nil, nil, nil)
	future := repository.Match{
		ID: "m-future", Status: repository.StatusScheduled,
		KickoffAt: time.Now().Add(5 * 24 * time.Hour),
	}
	if s.shouldRefresh(future) {
		t.Fatal("scheduled match 5 days out must not trigger a refresh")
	}
	near := repository.Match{
		ID: "m-near", Status: repository.StatusScheduled,
		KickoffAt: time.Now().Add(30 * time.Minute),
	}
	if !s.shouldRefresh(near) {
		t.Fatal("scheduled match in 30 minutes should trigger a refresh")
	}
	finished := repository.Match{ID: "m-done", Status: repository.StatusFinished}
	if s.shouldRefresh(finished) {
		t.Fatal("finished match must not trigger a refresh")
	}
}

func TestShouldRefresh_ConcurrentCallersDedupeToOne(t *testing.T) {
	// The whole point of the per-match dedupe window: if 20 viewers of
	// the same live match poll in the same instant, exactly one of
	// them should see shouldRefresh==true. Any other outcome means we
	// multiplied our API cost N× on first-paint.
	s := New(nil, nil, nil, nil)
	m := repository.Match{ID: "m-concurrent", Status: repository.StatusLive}

	var trueCount int64
	var wg sync.WaitGroup
	const callers = 20
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if s.shouldRefresh(m) {
				atomic.AddInt64(&trueCount, 1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := atomic.LoadInt64(&trueCount); got != 1 {
		t.Fatalf("concurrent dedupe failed: want exactly 1 refresh across %d callers, got %d", callers, got)
	}
}

// --- end-to-end style: Match() hits the refresher at most once per window ---

type countingRefresher struct {
	calls int64
	err   error
}

func (c *countingRefresher) RefreshMatch(_ context.Context, _ string) error {
	atomic.AddInt64(&c.calls, 1)
	return c.err
}

func (c *countingRefresher) Count() int64 { return atomic.LoadInt64(&c.calls) }

// (A full Service.Match integration test would require stubbing out the
// four repository types, which is more plumbing than this suite warrants
// right now; the shouldRefresh unit tests above cover the behaviour that
// gates upstream traffic. Keeping the stub type here so a future test
// against a real sqlmock-backed Service can drop in without churn.)
var _ MatchRefresher = (*countingRefresher)(nil)
