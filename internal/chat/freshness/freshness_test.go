package freshness

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
)

// newTestDB returns an in-memory DB used to seed sync_state rows for the
// freshness checker. The schema is created by storage.OpenPath.
func newTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenPath(":memory:")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// stubSyncer returns a Syncer that records its invocations and yields a
// canned (added, err) pair.
type stubSyncer struct {
	added int
	err   error
	calls int32
}

func (s *stubSyncer) fn() Syncer {
	return func(_ context.Context) (int, error) {
		atomic.AddInt32(&s.calls, 1)
		return s.added, s.err
	}
}

// findEvent returns the first event for source matching status, or nil.
func findEvent(events []Event, source string, status Status) *Event {
	for i := range events {
		if events[i].Source == source && events[i].Status == status {
			return &events[i]
		}
	}
	return nil
}

// ─── disabled → no-op ────────────────────────────────────────────────────────

func TestRun_DisabledIsNoOp(t *testing.T) {
	db := newTestDB(t)
	c := New(db, Options{Enabled: false, DefaultTTL: time.Hour})

	syncer := &stubSyncer{added: 5}
	events := Collect(c.Run(context.Background(), map[string]Syncer{
		"git": syncer.fn(),
	}, false))

	if len(events) != 0 {
		t.Errorf("expected no events when disabled, got %+v", events)
	}
	if atomic.LoadInt32(&syncer.calls) != 0 {
		t.Errorf("syncer should not run when disabled")
	}
}

// A forced run bypasses Enabled=false.
func TestRun_DisabledForceBypasses(t *testing.T) {
	db := newTestDB(t)
	c := New(db, Options{Enabled: false, DefaultTTL: time.Hour})

	syncer := &stubSyncer{added: 3}
	events := Collect(c.Run(context.Background(), map[string]Syncer{
		"git": syncer.fn(),
	}, true))

	if atomic.LoadInt32(&syncer.calls) != 1 {
		t.Errorf("forced run should call syncer once")
	}
	if findEvent(events, "git", StatusSyncing) == nil {
		t.Errorf("expected syncing event, got %+v", events)
	}
	synced := findEvent(events, "git", StatusSynced)
	if synced == nil || synced.Added != 3 {
		t.Errorf("expected synced event with Added=3, got %+v", events)
	}
}

// ─── fresh → silent ──────────────────────────────────────────────────────────

func TestRun_FreshSourceIsSilent(t *testing.T) {
	db := newTestDB(t)
	if err := db.SetSyncState("git", ""); err != nil {
		t.Fatalf("SetSyncState: %v", err)
	}

	// SetSyncState writes a current "now"; pin Now to ten minutes later
	// so age = 10m, well under the 1h TTL.
	c := New(db, Options{
		Enabled:    true,
		DefaultTTL: time.Hour,
		Now:        func() time.Time { return time.Now().Add(10 * time.Minute) },
	})

	syncer := &stubSyncer{added: 9}
	events := Collect(c.Run(context.Background(), map[string]Syncer{
		"git": syncer.fn(),
	}, false))

	if len(events) != 0 {
		t.Errorf("fresh source should emit no events, got %+v", events)
	}
	if atomic.LoadInt32(&syncer.calls) != 0 {
		t.Errorf("fresh source should not invoke syncer")
	}
}

// ─── stale → triggers syncer ─────────────────────────────────────────────────

func TestRun_StaleSourceTriggersSyncer(t *testing.T) {
	db := newTestDB(t)
	if err := db.SetSyncState("slack", ""); err != nil {
		t.Fatalf("SetSyncState: %v", err)
	}

	// Simulate "now" 4 hours after the recorded sync — well past the
	// 3h default TTL.
	c := New(db, Options{
		Enabled:    true,
		DefaultTTL: 3 * time.Hour,
		Now:        func() time.Time { return time.Now().Add(4 * time.Hour) },
	})

	syncer := &stubSyncer{added: 12}
	events := Collect(c.Run(context.Background(), map[string]Syncer{
		"slack": syncer.fn(),
	}, false))

	if atomic.LoadInt32(&syncer.calls) != 1 {
		t.Errorf("stale source should invoke syncer exactly once, calls=%d", syncer.calls)
	}
	if findEvent(events, "slack", StatusSyncing) == nil {
		t.Errorf("expected syncing event")
	}
	if ev := findEvent(events, "slack", StatusSynced); ev == nil || ev.Added != 12 {
		t.Errorf("expected synced event with Added=12, got %+v", events)
	}
}

// ─── never-synced source counts as stale ─────────────────────────────────────

func TestRun_NeverSyncedIsStale(t *testing.T) {
	db := newTestDB(t)
	c := New(db, Options{Enabled: true, DefaultTTL: time.Hour})

	syncer := &stubSyncer{added: 1}
	_ = Collect(c.Run(context.Background(), map[string]Syncer{
		"git": syncer.fn(),
	}, false))

	if atomic.LoadInt32(&syncer.calls) != 1 {
		t.Errorf("never-synced source should be treated as stale")
	}
}

// ─── per-source TTL override ─────────────────────────────────────────────────

func TestRun_PerSourceTTL(t *testing.T) {
	db := newTestDB(t)
	if err := db.SetSyncState("git", ""); err != nil {
		t.Fatalf("SetSyncState: %v", err)
	}

	c := New(db, Options{
		Enabled:    true,
		DefaultTTL: time.Hour,
		TTLs:       map[string]time.Duration{"git": 5 * time.Minute},
		// 10m later: stale under per-source 5m, fresh under default 1h.
		Now: func() time.Time { return time.Now().Add(10 * time.Minute) },
	})

	syncer := &stubSyncer{}
	_ = Collect(c.Run(context.Background(), map[string]Syncer{
		"git": syncer.fn(),
	}, false))

	if atomic.LoadInt32(&syncer.calls) != 1 {
		t.Errorf("per-source TTL override should mark git stale, calls=%d", syncer.calls)
	}
}

// ─── syncer error → error event, no panic ────────────────────────────────────

func TestRun_SyncerError(t *testing.T) {
	db := newTestDB(t)
	c := New(db, Options{Enabled: true, DefaultTTL: time.Hour})

	syncer := func(_ context.Context) (int, error) {
		return 0, errors.New("rate-limited")
	}
	events := Collect(c.Run(context.Background(), map[string]Syncer{
		"slack": syncer,
	}, false))

	ev := findEvent(events, "slack", StatusError)
	if ev == nil {
		t.Fatalf("expected error event, got %+v", events)
	}
	if ev.Err != "rate-limited" {
		t.Errorf("error event Err = %q", ev.Err)
	}
	if findEvent(events, "slack", StatusSynced) != nil {
		t.Errorf("should not also emit synced event on failure")
	}
}

// ─── parallel fan-out ────────────────────────────────────────────────────────

func TestRun_ParallelFanOut(t *testing.T) {
	db := newTestDB(t)
	c := New(db, Options{Enabled: true, DefaultTTL: time.Hour, Wait: 2 * time.Second})

	var mu sync.Mutex
	concurrent := 0
	maxConcurrent := 0
	syncer := func(_ context.Context) (int, error) {
		mu.Lock()
		concurrent++
		if concurrent > maxConcurrent {
			maxConcurrent = concurrent
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		concurrent--
		mu.Unlock()
		return 1, nil
	}

	_ = Collect(c.Run(context.Background(), map[string]Syncer{
		"git":   syncer,
		"slack": syncer,
		"jira":  syncer,
	}, false))

	if maxConcurrent < 2 {
		t.Errorf("expected fan-out, max concurrent = %d", maxConcurrent)
	}
}

// ─── wait timeout cancels in-flight syncer via context ───────────────────────

func TestRun_WaitTimeout(t *testing.T) {
	db := newTestDB(t)
	c := New(db, Options{
		Enabled:    true,
		DefaultTTL: time.Hour,
		Wait:       20 * time.Millisecond,
	})

	syncer := func(ctx context.Context) (int, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(200 * time.Millisecond):
			return 1, nil
		}
	}
	events := Collect(c.Run(context.Background(), map[string]Syncer{
		"git": syncer,
	}, false))

	if findEvent(events, "git", StatusError) == nil {
		t.Errorf("expected error event from wait timeout, got %+v", events)
	}
}

// ─── empty syncers map closes channel immediately ────────────────────────────

func TestRun_NoSyncers(t *testing.T) {
	c := New(nil, Options{Enabled: true})
	events := Collect(c.Run(context.Background(), nil, false))
	if len(events) != 0 {
		t.Errorf("expected no events, got %+v", events)
	}
}
