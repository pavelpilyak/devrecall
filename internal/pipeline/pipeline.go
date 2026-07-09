// Package pipeline is the single post-sync processing entry point. After
// collectors store raw activities, PostSync turns them into the structured
// knowledge base: deterministic work-item linking, LLM enrichment
// (digest + tags per activity), and vector embeddings — in that order, so
// fresh activities embed with their digest included.
//
// Every stage is non-fatal: a missing LLM or embedding provider skips its
// stage silently, and failures surface as warnings on the log writer while
// the remaining stages still run.
package pipeline

import (
	"context"
	"fmt"
	"io"

	"github.com/pavelpilyak/devrecall/internal/auth"
	"github.com/pavelpilyak/devrecall/internal/config"
	"github.com/pavelpilyak/devrecall/internal/embedding"
	"github.com/pavelpilyak/devrecall/internal/enrich"
	"github.com/pavelpilyak/devrecall/internal/llm"
	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/internal/workitem"
)

// Options selects which post-sync stages run.
type Options struct {
	Link   bool // materialize work items (fast, no LLM)
	Enrich bool // LLM digest + tags for new activities
	Embed  bool // vector embeddings for new activities
	Log    io.Writer
}

// PostSync runs the enabled stages. It never returns an error for a stage
// that merely lacks configuration (no LLM provider, no embedder); genuine
// failures are printed as warnings and do not stop later stages.
func PostSync(ctx context.Context, db *storage.DB, cfg *config.Config, tokenStore auth.TokenStore, opts Options) {
	log := opts.Log
	if log == nil {
		log = io.Discard
	}

	if opts.Link {
		if stats, err := workitem.Materialize(db); err != nil {
			fmt.Fprintf(log, "Work-item linking warning: %v\n", err)
		} else if stats.WorkItems > 0 {
			fmt.Fprintf(log, "Linked %d work items (%d activity links).\n", stats.WorkItems, stats.Links)
		}
	}

	if opts.Enrich && cfg.LLM.EnrichPerSync >= 0 {
		if provider, err := llm.FromConfig(cfg, tokenStore); err == nil {
			enricher := enrich.New(provider)
			stats, err := enricher.Run(ctx, db, enrich.Options{MaxPerRun: cfg.LLM.EnrichPerSync})
			if err != nil {
				fmt.Fprintf(log, "Enrichment warning: %v\n", err)
			}
			if n := stats.Deterministic + stats.LLM + stats.Fallback; n > 0 {
				fmt.Fprintf(log, "Enriched %d activities (%d via LLM).\n", n, stats.LLM)
			}
		}
	}

	if opts.Embed {
		if embedder, err := embedding.FromConfig(cfg, tokenStore); err == nil {
			if err := embedMissing(ctx, db, embedder, log); err != nil {
				fmt.Fprintf(log, "Embedding warning: %v\n", err)
			}
		}
	}
}

// embedMissing generates vector embeddings for activities that lack them.
// When an enrichment exists for an activity, its digest and tags are
// appended to the embedded text so semantic search sees the normalized
// summary, not just raw title+content.
func embedMissing(ctx context.Context, db *storage.DB, embedder embedding.Embedder, log io.Writer) error {
	const batchSize = 50
	totalEmbedded := 0

	for {
		ids, err := db.ListUnembeddedActivityIDs(batchSize)
		if err != nil {
			return fmt.Errorf("list unembedded: %w", err)
		}
		if len(ids) == 0 {
			break
		}

		if totalEmbedded == 0 {
			// Count total unembedded for progress on first batch.
			allIDs, _ := db.ListUnembeddedActivityIDs(0)
			fmt.Fprintf(log, "Embedding %d activities...\n", len(allIDs))
		}

		activities, err := db.GetActivitiesByIDs(ids)
		if err != nil {
			return fmt.Errorf("get activities: %w", err)
		}
		enrichments, err := db.GetEnrichmentsByActivityIDs(ids)
		if err != nil {
			return fmt.Errorf("get enrichments: %w", err)
		}

		texts := make([]string, len(activities))
		for i, a := range activities {
			text := a.Title
			if a.Content != "" {
				text += " " + a.Content
			}
			if e, ok := enrichments[a.ID]; ok {
				if e.Digest != "" && e.Digest != a.Title {
					text += " " + e.Digest
				}
				for _, tag := range e.Tags {
					text += " " + tag
				}
			}
			texts[i] = text
		}

		vectors, err := embedder.EmbedBatch(ctx, texts)
		if err != nil {
			return fmt.Errorf("embed batch: %w", err)
		}

		for i, a := range activities {
			if err := db.InsertEmbedding(a.ID, embedder.Name(), vectors[i]); err != nil {
				return fmt.Errorf("store embedding for activity %d: %w", a.ID, err)
			}
		}

		totalEmbedded += len(activities)
		fmt.Fprintf(log, "  Embedded %d activities...\n", totalEmbedded)
	}

	if totalEmbedded > 0 {
		fmt.Fprintf(log, "Embedding complete: %d activities.\n", totalEmbedded)
	}

	return nil
}
