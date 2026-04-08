// Package freshness implements the pre-agent sync step described in
// docs/chat-agent-rewrite.md ("Sync freshness — pre-agent only").
//
// Before the chat agent loop runs, the caller asks a Checker to inspect
// the per-source `sync_state.synced_at` timestamps and trigger a fast
// incremental sync for any source whose TTL has lapsed. The Checker
// streams Events as the work happens so the UI (CLI or SSE handler)
// can render "Syncing slack…" / "slack synced (12 new)" lines without
// blocking the user any longer than the configured Wait cap.
//
// The Checker is intentionally decoupled from collectors: it takes a
// map of `Syncer` callbacks the caller wires up against the existing
// per-source sync helpers in internal/api or internal/collector/*.
// Tests use plain function literals — no DB or network required.
package freshness

import (
	"context"
	"sync"
	"time"

	"github.com/pavelpiliak/devrecall/internal/storage"
)

// Status enumerates the lifecycle of a source during a freshness check.
type Status string

const (
	// StatusFresh: synced within the TTL window — nothing to do.
	StatusFresh Status = "fresh"
	// StatusSyncing: a stale source has begun an incremental sync.
	StatusSyncing Status = "syncing"
	// StatusSynced: incremental sync completed successfully.
	StatusSynced Status = "synced"
	// StatusError: incremental sync failed; the agent proceeds anyway.
	StatusError Status = "error"
	// StatusDisabled: the freshness step is disabled by config; emitted
	// only when the caller explicitly asks for a forced refresh and the
	// step is off (so /sync still has a way to surface the no-op).
	StatusDisabled Status = "disabled"
	// StatusSkipped: source has no syncer registered (collector missing
	// or disabled in config); silently skipped during normal runs but
	// reported when forcing.
	StatusSkipped Status = "skipped"
)

// Event is one update emitted on the channel returned by Run.
//
// Source identifies the data source ("git", "slack", …). Status is the
// new lifecycle state. Age is the wall-clock distance between Now() and
// the source's last sync at the moment the check was made.
type Event struct {
	Source string        `json:"source"`
	Status Status        `json:"status"`
	Age    time.Duration `json:"age,omitempty"`
	Added  int           `json:"added,omitempty"`
	Err    string        `json:"error,omitempty"`
}

// Syncer runs an incremental sync for one source and returns the number
// of activities added. It must be safe to invoke concurrently with other
// syncers (the Checker fans them out).
type Syncer func(ctx context.Context) (added int, err error)

// Options configures a Checker.
//
// Enabled=false makes Run a no-op for normal calls; the caller can still
// pass force=true to bypass.
type Options struct {
	// Enabled toggles the entire freshness step. When false, normal Run
	// calls return immediately with no events.
	Enabled bool
	// DefaultTTL is the freshness window applied to any source not
	// listed in TTLs. Zero or negative means "always stale".
	DefaultTTL time.Duration
	// TTLs lets you override the default per source ("slack" → 1h).
	TTLs map[string]time.Duration
	// Wait caps the total time the Checker will block waiting for in-
	// flight syncs to complete. Stragglers are cancelled via context.
	// Zero falls back to a sane default.
	Wait time.Duration
	// Now lets tests pin a deterministic clock. nil → time.Now.
	Now func() time.Time
}

const (
	// DefaultTTL is the freshness window applied when Options.DefaultTTL is zero.
	DefaultTTL = 3 * time.Hour
	// DefaultWait is the per-Run wait cap when Options.Wait is zero.
	DefaultWait = 10 * time.Second
)

// Checker drives the freshness step. Construct one per chat session and
// reuse it across queries — Run is goroutine-safe.
type Checker struct {
	db   *storage.DB
	opts Options
}

// New constructs a Checker. The db handle is consulted for sync_state
// rows; pass nil only in tests where every source is treated as never
// synced.
func New(db *storage.DB, opts Options) *Checker {
	if opts.DefaultTTL == 0 {
		opts.DefaultTTL = DefaultTTL
	}
	if opts.Wait <= 0 {
		opts.Wait = DefaultWait
	}
	return &Checker{db: db, opts: opts}
}

// Run inspects each provided syncer's freshness and triggers a parallel
// sync for any that have exceeded their TTL.
//
// The returned channel emits Events as work progresses and is closed
// when every triggered sync has finished or the Wait cap has elapsed.
// If Options.Enabled is false and force is false, the channel is
// returned already closed with no events. Pass force=true (e.g. from a
// /sync slash command) to ignore both the enabled flag and TTLs and
// re-sync every registered source unconditionally.
//
// Callers should drain the channel; abandoning it leaks goroutines
// until ctx is cancelled.
func (c *Checker) Run(ctx context.Context, syncers map[string]Syncer, force bool) <-chan Event {
	out := make(chan Event, len(syncers)+1)

	if !force && !c.opts.Enabled {
		close(out)
		return out
	}
	if len(syncers) == 0 {
		close(out)
		return out
	}

	now := time.Now
	if c.opts.Now != nil {
		now = c.opts.Now
	}

	// Decide which sources need syncing. Fresh sources don't emit any
	// event during a normal run, so the UI stays quiet when everything
	// is up to date. A forced run emits a fresh/skipped event for every
	// source so the user sees what /sync did.
	var toSync []string
	for source := range syncers {
		ttl := c.opts.DefaultTTL
		if v, ok := c.opts.TTLs[source]; ok {
			ttl = v
		}

		var lastSync time.Time
		if c.db != nil {
			st, _ := c.db.GetSyncState(source)
			if st != nil {
				lastSync = st.SyncedAt
			}
		}
		age := time.Duration(0)
		if !lastSync.IsZero() {
			age = now().Sub(lastSync)
		}

		stale := lastSync.IsZero() || age >= ttl
		if force {
			stale = true
		}
		if !stale {
			if force {
				out <- Event{Source: source, Status: StatusFresh, Age: age}
			}
			continue
		}
		toSync = append(toSync, source)
	}

	if len(toSync) == 0 {
		close(out)
		return out
	}

	// Bound the whole fan-out by Wait so a slow collector can't hold
	// the chat indefinitely. Stragglers see ctx.Done() and bail.
	waitCtx, cancel := context.WithTimeout(ctx, c.opts.Wait)

	var wg sync.WaitGroup
	for _, source := range toSync {
		source := source
		syncer := syncers[source]
		wg.Add(1)
		go func() {
			defer wg.Done()
			out <- Event{Source: source, Status: StatusSyncing}
			added, err := syncer(waitCtx)
			if err != nil {
				out <- Event{Source: source, Status: StatusError, Err: err.Error()}
				return
			}
			out <- Event{Source: source, Status: StatusSynced, Added: added}
		}()
	}

	go func() {
		wg.Wait()
		cancel()
		close(out)
	}()

	return out
}

// Collect drains an Event channel into a slice. Convenience helper for
// tests and non-streaming callers.
func Collect(ch <-chan Event) []Event {
	var out []Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}
