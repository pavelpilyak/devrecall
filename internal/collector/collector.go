package collector

import (
	"context"

	"github.com/pavelpilyak/devrecall/pkg/models"
)

// Collector defines the interface all source integrations must implement.
type Collector interface {
	// Name returns the source name (e.g., "git", "slack").
	Name() models.Source

	// Collect fetches new activities since the last sync.
	Collect(ctx context.Context) ([]models.Activity, error)
}
