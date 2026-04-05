package summarizer

import (
	"github.com/pavelpiliak/devrecall/pkg/models"
)

// Summarizer generates human-readable summaries from activities.
type Summarizer interface {
	// Standup generates a standup report from the given activities.
	Standup(activities []models.Activity) (string, error)

	// WeeklySummary generates a weekly summary with per-day breakdown and meeting time stats.
	WeeklySummary(activities []models.Activity) (string, error)

	// BragDoc generates a brag document highlighting key accomplishments for a period.
	BragDoc(activities []models.Activity, childSummaries []models.Summary) (string, error)
}
