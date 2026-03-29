package git

import (
	"context"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

// Collector gathers commit activity from local git repositories.
type Collector struct {
	repos  []string
	emails []string
}

// New creates a git collector for the given repos and author emails.
func New(repos, emails []string) *Collector {
	return &Collector{repos: repos, emails: emails}
}

func (c *Collector) Name() models.Source {
	return models.SourceGit
}

func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	// TODO: implement git log parsing
	return nil, nil
}
