package rag

import (
	"context"
	"time"

	"github.com/pavelpiliak/devrecall/internal/embedding"
	"github.com/pavelpiliak/devrecall/internal/storage"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

// QueryFilters controls which activities are eligible for retrieval.
type QueryFilters struct {
	Source     models.Source
	IdentityID int64
	After      time.Time
	Before     time.Time
}

// HybridRetriever combines vector search and FTS5 keyword search,
// merging results with Reciprocal Rank Fusion (RRF).
type HybridRetriever struct {
	db       *storage.DB
	embedder embedding.Embedder
}

// NewHybridRetriever creates a retriever that uses both semantic and keyword search.
func NewHybridRetriever(db *storage.DB, embedder embedding.Embedder) *HybridRetriever {
	return &HybridRetriever{db: db, embedder: embedder}
}

// Retrieve finds activities relevant to a natural language query.
// It runs vector search + FTS5 keyword search and merges with RRF.
func (h *HybridRetriever) Retrieve(ctx context.Context, query string, limit int) ([]Result, error) {
	return h.RetrieveWithFilters(ctx, query, limit, QueryFilters{})
}

// RetrieveWithFilters is like Retrieve but with additional source/identity/date constraints.
func (h *HybridRetriever) RetrieveWithFilters(ctx context.Context, query string, limit int, filters QueryFilters) ([]Result, error) {
	if limit <= 0 {
		limit = 10
	}

	// Fetch more candidates from each source to give RRF enough to merge.
	candidateLimit := limit * 3
	if candidateLimit < 20 {
		candidateLimit = 20
	}

	// Run vector search and FTS5 search.
	var vecResults []storage.VectorMatch
	var ftsResults []storage.FTSMatch
	var vecErr, ftsErr error

	// Vector search (needs embedding the query first).
	queryVec, vecErr := h.embedder.Embed(ctx, query)
	if vecErr == nil {
		vecResults, vecErr = h.db.SearchSimilar(queryVec, candidateLimit, filters.After, filters.Before)
	}

	// FTS5 keyword search.
	ftsFilter := storage.ActivityFilter{
		Source:     filters.Source,
		IdentityID: filters.IdentityID,
		After:      filters.After,
		Before:     filters.Before,
	}
	ftsResults, ftsErr = h.db.SearchFTS(query, ftsFilter, candidateLimit)

	// If both fail, return the error. If only one fails, use the other.
	if vecErr != nil && ftsErr != nil {
		return nil, vecErr
	}

	// Merge with Reciprocal Rank Fusion.
	merged := reciprocalRankFusion(vecResults, ftsResults, filters.Source, filters.IdentityID)

	// Truncate to limit.
	if len(merged) > limit {
		merged = merged[:limit]
	}

	return merged, nil
}

// reciprocalRankFusion merges vector and FTS results using RRF scoring.
// RRF score = sum of 1/(k + rank) for each list the item appears in.
// k=60 is the standard constant that reduces the impact of high rankings.
func reciprocalRankFusion(vecResults []storage.VectorMatch, ftsResults []storage.FTSMatch, sourceFilter models.Source, identityFilter int64) []Result {
	const k = 60.0

	type candidate struct {
		activity models.Activity
		rrfScore float64
	}
	byID := make(map[int64]*candidate)

	// Score vector results by rank position.
	for rank, vm := range vecResults {
		// Apply source/identity filters that vec search doesn't handle.
		if sourceFilter != "" && vm.Activity.Source != sourceFilter {
			continue
		}
		if identityFilter > 0 && vm.Activity.IdentityID != identityFilter {
			continue
		}

		id := vm.Activity.ID
		if c, ok := byID[id]; ok {
			c.rrfScore += 1.0 / (k + float64(rank))
		} else {
			byID[id] = &candidate{
				activity: vm.Activity,
				rrfScore: 1.0 / (k + float64(rank)),
			}
		}
	}

	// Score FTS results by rank position.
	for rank, fm := range ftsResults {
		id := fm.Activity.ID
		if c, ok := byID[id]; ok {
			c.rrfScore += 1.0 / (k + float64(rank))
		} else {
			byID[id] = &candidate{
				activity: fm.Activity,
				rrfScore: 1.0 / (k + float64(rank)),
			}
		}
	}

	// Collect and sort by RRF score descending.
	results := make([]Result, 0, len(byID))
	for _, c := range byID {
		results = append(results, Result{
			Activity: c.activity,
			Score:    c.rrfScore,
		})
	}

	// Sort descending by score.
	for i := 0; i < len(results); i++ {
		maxIdx := i
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[maxIdx].Score {
				maxIdx = j
			}
		}
		results[i], results[maxIdx] = results[maxIdx], results[i]
	}

	return results
}
