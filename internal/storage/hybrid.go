package storage

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

// rrfK is the smoothing constant in Reciprocal Rank Fusion. 60 is the value
// from Cormack et al. (2009) and the de-facto default: large enough that the
// top few ranks don't dominate outright, small enough that deep results still
// contribute meaningfully.
const rrfK = 60.0

// HybridMatch is a single fused search result. FTSRank and VecRank record where
// the activity placed in each arm (1-based, 0 = absent), which makes it possible
// to explain *why* something surfaced — useful for debugging retrieval quality.
type HybridMatch struct {
	Activity models.Activity
	Score    float64 // RRF score; higher is better
	FTSRank  int
	VecRank  int
}

// HybridSearch runs keyword (FTS5/BM25) and vector (KNN) search over the same
// filter and fuses the two ranked lists with Reciprocal Rank Fusion.
//
// Fusing by *rank* rather than by score is deliberate: BM25 ranks and cosine
// similarities live on incomparable scales, so any weighted blend of the raw
// numbers needs arbitrary normalization constants that drift as the corpus
// grows. RRF only reads ordinal position, so it composes the two arms without
// tuning and degrades gracefully when one arm returns nothing.
//
// queryVec may be nil (keyword-only) and query may be empty (vector-only); if
// both are empty the result is empty. Passing the vector in — rather than
// embedding here — keeps this package free of any embedding dependency.
func (db *DB) HybridSearch(query string, queryVec []float32, filter ActivityFilter, limit int) ([]HybridMatch, error) {
	if limit <= 0 {
		limit = 20
	}
	query = strings.TrimSpace(query)
	if query == "" && len(queryVec) == 0 {
		return nil, nil
	}

	// Pull deeper than `limit` from each arm so fusion has material to work
	// with: an item ranked #25 by keyword and #3 by vector should be able to
	// win, which can't happen if each list is truncated at the output limit.
	candidates := limit * 3
	if candidates < 30 {
		candidates = 30
	}

	fused := make(map[int64]*HybridMatch)

	if query != "" {
		ftsHits, err := db.SearchFTS(query, filter, candidates)
		if err != nil {
			return nil, fmt.Errorf("hybrid keyword arm: %w", err)
		}
		for i, hit := range ftsHits {
			rank := i + 1
			m := &HybridMatch{Activity: hit.Activity, FTSRank: rank}
			m.Score = 1.0 / (rrfK + float64(rank))
			fused[hit.Activity.ID] = m
		}
	}

	if len(queryVec) > 0 {
		// SearchSimilar only understands the date range, so the remaining
		// predicates (source/type/identity/tag) are applied here. Over-fetch to
		// leave enough survivors after that filtering.
		vecHits, err := db.SearchSimilar(queryVec, candidates*2, filter.After, filter.Before)
		if err != nil {
			return nil, fmt.Errorf("hybrid vector arm: %w", err)
		}

		var tagged map[int64]bool
		if filter.Tag != "" {
			tagged, err = db.activityIDsWithTag(filter.Tag)
			if err != nil {
				return nil, fmt.Errorf("hybrid tag filter: %w", err)
			}
		}

		rank := 0
		for _, hit := range vecHits {
			if !matchesFilter(hit.Activity, filter, tagged) {
				continue
			}
			rank++ // rank over surviving hits only, so filtering doesn't leave gaps
			if rank > candidates {
				break
			}
			contribution := 1.0 / (rrfK + float64(rank))
			if m, ok := fused[hit.Activity.ID]; ok {
				m.Score += contribution
				m.VecRank = rank
				continue
			}
			fused[hit.Activity.ID] = &HybridMatch{
				Activity: hit.Activity,
				Score:    contribution,
				VecRank:  rank,
			}
		}
	}

	out := make([]HybridMatch, 0, len(fused))
	for _, m := range fused {
		out = append(out, *m)
	}
	// Sort by score, then most-recent-first, then ID so ties are deterministic.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if !out[i].Activity.Timestamp.Equal(out[j].Activity.Timestamp) {
			return out[i].Activity.Timestamp.After(out[j].Activity.Timestamp)
		}
		return out[i].Activity.ID < out[j].Activity.ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// HasEmbeddings reports whether any embedding has been stored yet. Callers use
// it to skip embedding the query when the vector arm has nothing to match —
// which also avoids triggering a first-run ONNX model download for a search
// that would be keyword-only regardless. Cheaper than a full COUNT(*).
func (db *DB) HasEmbeddings() bool {
	var one int
	err := db.QueryRow(`SELECT 1 FROM embeddings LIMIT 1`).Scan(&one)
	return err == nil
}

// matchesFilter reports whether an activity satisfies the non-date predicates of
// a filter. Date bounds are already handled by SearchSimilar. When the filter
// carries a tag, `tagged` is the pre-resolved set of activity IDs holding it.
func matchesFilter(a models.Activity, f ActivityFilter, tagged map[int64]bool) bool {
	if f.Source != "" && a.Source != f.Source {
		return false
	}
	if f.Type != "" && a.Type != f.Type {
		return false
	}
	if f.IdentityID > 0 && a.IdentityID != f.IdentityID {
		return false
	}
	if f.Tag != "" && !tagged[a.ID] {
		return false
	}
	return true
}

// activityIDsWithTag returns the set of activity IDs carrying the given
// enrichment tag, so vector hits can be tag-filtered in memory.
func (db *DB) activityIDsWithTag(tag string) (map[int64]bool, error) {
	rows, err := db.Query(
		`SELECT e.activity_id FROM enrichments e, json_each(e.tags) jt WHERE jt.value = ?`,
		strings.ToLower(tag),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
