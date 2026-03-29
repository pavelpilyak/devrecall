package summarizer

import (
	"github.com/pavelpiliak/devrecall/pkg/models"
)

// Summarizer generates human-readable summaries from activities.
type Summarizer interface {
	// Standup generates a standup report from the given activities.
	Standup(activities []models.Activity) (string, error)
}
