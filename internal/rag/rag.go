package rag

import (
	"context"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

// Result is a single retrieved item with its relevance score.
type Result struct {
	Activity models.Activity
	Score    float64
}

// Retriever finds activities relevant to a natural language query.
type Retriever interface {
	// Retrieve performs hybrid search (vector + FTS5 + filters) and returns ranked results.
	Retrieve(ctx context.Context, query string, limit int) ([]Result, error)
}
