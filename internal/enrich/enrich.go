// Package enrich digests activities into an AI-friendly shape at ingest
// time: a one-line factual digest plus classification tags per activity,
// stored in the enrichments table. It runs as a post-sync batch pass —
// idempotent (row presence marks an activity done), incremental (newest
// first, capped per run), and non-fatal on every failure path.
package enrich

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/internal/workitem"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// Vocabulary is the controlled tag set the LLM classifies into. Up to two
// free-form tags are allowed on top.
var Vocabulary = []string{
	"feature", "bugfix", "refactor", "review", "testing", "docs", "deploy",
	"release", "incident", "planning", "discussion", "decision", "meeting",
	"dependency", "security", "performance",
}

const (
	defaultBatchPerCall = 8
	defaultMaxPerRun    = 300
	maxFreeFormTags     = 2
	maxTagLen           = 32
	maxContentChars     = 600
)

const systemPrompt = `You digest developer activity events into structured metadata. For each numbered activity, produce:
- "digest": one factual sentence, past tense, stating what happened. No filler, no speculation.
- "tags": 1-4 tags. Prefer these: %s. You may add up to 2 short free-form tags if none fit.
- "entities": optional {"people": [...], "systems": [...]} mentioned by name.

Respond ONLY with a JSON array (no prose, no code fences):
[{"n": 1, "digest": "...", "tags": ["..."], "entities": {"people": [], "systems": []}}, ...]
Include every numbered activity exactly once.`

// Options controls an enrichment run.
type Options struct {
	BatchPerCall int // activities per LLM call (default 8)
	MaxPerRun    int // total activities per run (default 300)
}

// Stats reports what a run produced.
type Stats struct {
	Deterministic int // rule-based pre-fills, no LLM call
	LLM           int // successfully digested by the LLM
	Fallback      int // placeholder rows after unusable LLM output
}

// Enricher runs the LLM enrichment pass.
type Enricher struct {
	provider llm.Provider
}

// New creates an Enricher backed by the given provider.
func New(provider llm.Provider) *Enricher {
	return &Enricher{provider: provider}
}

// enrichMeta is the slice of activity metadata the deterministic
// pre-fills read.
type enrichMeta struct {
	ToStatus string   `json:"to_status"`
	Tags     []string `json:"tags"`
}

type enrichItem struct {
	N        int             `json:"n"`
	Digest   string          `json:"digest"`
	Tags     []string        `json:"tags"`
	Entities json.RawMessage `json:"entities"`
}

// Run enriches unenriched activities, newest first, up to MaxPerRun.
// Deterministic classes (status transitions, manual notes) are filled
// without an LLM call. An LLM error aborts the run — the remaining rows
// stay unenriched and are retried on the next sync.
func (e *Enricher) Run(ctx context.Context, db *storage.DB, opts Options) (Stats, error) {
	if opts.BatchPerCall <= 0 {
		opts.BatchPerCall = defaultBatchPerCall
	}
	if opts.MaxPerRun <= 0 {
		opts.MaxPerRun = defaultMaxPerRun
	}

	var stats Stats
	ids, err := db.ListUnenrichedActivityIDs(opts.MaxPerRun)
	if err != nil {
		return stats, err
	}
	if len(ids) == 0 {
		return stats, nil
	}
	activities, err := db.GetActivitiesByIDs(ids)
	if err != nil {
		return stats, err
	}
	// GetActivitiesByIDs gives no order guarantee; keep newest first so a
	// capped run digests recent activity before backlog.
	sort.Slice(activities, func(i, j int) bool {
		return activities[i].Timestamp.After(activities[j].Timestamp)
	})

	// Deterministic pre-fills first: they cover the highest-volume noise
	// (status transitions) without spending LLM calls, and copy manual
	// note tags that were previously write-only.
	var pending []models.Activity
	var prefilled []storage.Enrichment
	for _, a := range activities {
		if enr, ok := deterministicEnrichment(a); ok {
			prefilled = append(prefilled, enr)
			continue
		}
		pending = append(pending, a)
	}
	if len(prefilled) > 0 {
		if _, err := db.InsertEnrichments(prefilled); err != nil {
			return stats, err
		}
		stats.Deterministic = len(prefilled)
	}

	for start := 0; start < len(pending); start += opts.BatchPerCall {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		end := start + opts.BatchPerCall
		if end > len(pending) {
			end = len(pending)
		}
		batch := pending[start:end]

		results, err := e.digestBatch(ctx, batch)
		if err != nil {
			// One retry per batch, then abort the run: undigested rows
			// stay unenriched and the next sync picks them up.
			results, err = e.digestBatch(ctx, batch)
			if err != nil {
				return stats, fmt.Errorf("enrichment batch: %w", err)
			}
		}

		rows := make([]storage.Enrichment, 0, len(batch))
		for i, a := range batch {
			if r, ok := results[i]; ok {
				rows = append(rows, r)
				stats.LLM++
				continue
			}
			// The LLM skipped or garbled this item; write a fallback row
			// so the pipeline converges instead of retrying the same
			// activity every sync.
			rows = append(rows, storage.Enrichment{
				ActivityID: a.ID,
				Digest:     a.Title,
				Tags:       []string{string(a.Type)},
				Model:      storage.EnrichmentModelFallback,
			})
			stats.Fallback++
		}
		if _, err := db.InsertEnrichments(rows); err != nil {
			return stats, err
		}
	}

	return stats, nil
}

// deterministicEnrichment returns a rule-based enrichment for activity
// classes that don't need an LLM, and ok=false otherwise.
func deterministicEnrichment(a models.Activity) (storage.Enrichment, bool) {
	var meta enrichMeta
	if a.Metadata != "" {
		json.Unmarshal([]byte(a.Metadata), &meta)
	}

	// Status transitions ("moved PROJ-123 to Done") are structured facts,
	// not work — classify them without asking a model.
	if meta.ToStatus != "" {
		subject := "ticket"
		if keys := workitem.ExtractIssueKeys(a.Metadata); len(keys) > 0 {
			subject = keys[0]
		}
		return storage.Enrichment{
			ActivityID: a.ID,
			Digest:     fmt.Sprintf("Moved %s to %s", subject, meta.ToStatus),
			Tags:       []string{"status-change"},
			Model:      storage.EnrichmentModelDeterministic,
		}, true
	}

	// Manual notes already carry user-chosen tags in metadata.
	if a.Source == models.SourceManual {
		tags := clampTags(meta.Tags)
		if len(tags) == 0 {
			tags = []string{"note"}
		}
		return storage.Enrichment{
			ActivityID: a.ID,
			Digest:     a.Title,
			Tags:       tags,
			Model:      storage.EnrichmentModelDeterministic,
		}, true
	}

	return storage.Enrichment{}, false
}

// digestBatch sends one numbered batch to the LLM and returns parsed
// enrichments keyed by batch index. Missing or invalid items are simply
// absent from the map.
func (e *Enricher) digestBatch(ctx context.Context, batch []models.Activity) (map[int]storage.Enrichment, error) {
	var b strings.Builder
	for i, a := range batch {
		content := a.Content
		if len(content) > maxContentChars {
			content = content[:maxContentChars]
		}
		fmt.Fprintf(&b, "%d. [%s %s] %s", i+1, a.Source, a.Type, a.Title)
		if content != "" && content != a.Title {
			fmt.Fprintf(&b, " — %s", content)
		}
		b.WriteString("\n")
	}

	resp, err := e.provider.Chat(ctx, []llm.Message{
		{Role: "system", Content: fmt.Sprintf(systemPrompt, strings.Join(Vocabulary, ", "))},
		{Role: "user", Content: b.String()},
	}, llm.ChatOpts{Temperature: 0.1, MaxTokens: 1024})
	if err != nil {
		return nil, err
	}

	var items []enrichItem
	if err := json.Unmarshal([]byte(llm.ExtractJSON(resp)), &items); err != nil {
		return nil, fmt.Errorf("parse enrichment response: %w", err)
	}

	results := make(map[int]storage.Enrichment, len(items))
	for _, it := range items {
		idx := it.N - 1
		if idx < 0 || idx >= len(batch) || strings.TrimSpace(it.Digest) == "" {
			continue
		}
		entities := ""
		if len(it.Entities) > 0 && string(it.Entities) != "null" {
			entities = string(it.Entities)
		}
		results[idx] = storage.Enrichment{
			ActivityID: batch[idx].ID,
			Digest:     strings.TrimSpace(it.Digest),
			Tags:       clampTags(it.Tags),
			Entities:   entities,
			Model:      e.provider.Name(),
		}
	}
	return results, nil
}

var vocabSet = func() map[string]bool {
	m := make(map[string]bool, len(Vocabulary))
	for _, v := range Vocabulary {
		m[v] = true
	}
	return m
}()

// clampTags lowercases and dedupes tags, passing vocabulary tags through
// and keeping at most maxFreeFormTags free-form ones (length-capped).
func clampTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	freeForm := 0
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || len(t) > maxTagLen || seen[t] {
			continue
		}
		if !vocabSet[t] {
			if freeForm >= maxFreeFormTags {
				continue
			}
			freeForm++
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
