package privacy

import (
	"encoding/json"

	"github.com/pavelpiliak/devrecall/internal/config"
	"github.com/pavelpiliak/devrecall/pkg/models"
)

// Apply filters a slice of activities according to per-source privacy modes.
// It returns a new slice — the originals are not modified.
func Apply(activities []models.Activity, cfg config.PrivacyConfig) []models.Activity {
	out := make([]models.Activity, 0, len(activities))
	for _, a := range activities {
		mode := cfg.ModeFor(string(a.Source))
		out = append(out, applyMode(a, mode))
	}
	return out
}

func applyMode(a models.Activity, mode config.PrivacyMode) models.Activity {
	switch mode {
	case config.PrivacyFull:
		return a
	case config.PrivacySummary:
		return applySummary(a)
	case config.PrivacyMetadata:
		return applyMetadataOnly(a)
	default:
		return a
	}
}

// applySummary keeps title and structured summaries but strips raw text content
// and verbose metadata fields (message bodies, thread messages, etc.).
func applySummary(a models.Activity) models.Activity {
	a.Content = ""

	switch a.Source {
	case models.SourceGit:
		// Keep repo, files_changed, insertions, deletions. Strip nothing extra —
		// git metadata is already summary-level.
	case models.SourceSlack:
		a.Metadata = stripSlackRawText(a.Metadata)
	}
	return a
}

// applyMetadataOnly strips title, content, and reduces metadata to basic counts.
func applyMetadataOnly(a models.Activity) models.Activity {
	a.Content = ""

	switch a.Source {
	case models.SourceGit:
		a.Title = ""
		a.Metadata = gitMetadataOnly(a.Metadata)
	case models.SourceSlack:
		a.Title = ""
		a.Metadata = slackMetadataOnly(a.Metadata)
	default:
		a.Title = ""
		a.Metadata = ""
	}
	return a
}

// gitMetadataOnly keeps only repo and file stats.
func gitMetadataOnly(meta string) string {
	var parsed struct {
		Repo         string `json:"repo"`
		FilesChanged int    `json:"files_changed"`
		Insertions   int    `json:"insertions"`
		Deletions    int    `json:"deletions"`
	}
	if err := json.Unmarshal([]byte(meta), &parsed); err != nil {
		return "{}"
	}
	out, _ := json.Marshal(parsed)
	return string(out)
}

// stripSlackRawText removes thread_msgs (raw message bodies) from Slack metadata
// but preserves the summary, channel info, and participant list.
func stripSlackRawText(meta string) string {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(meta), &parsed); err != nil {
		return meta
	}
	delete(parsed, "thread_msgs")
	out, _ := json.Marshal(parsed)
	return string(out)
}

// slackMetadataOnly keeps only channel_name and reply_count.
func slackMetadataOnly(meta string) string {
	var parsed struct {
		ChannelName string `json:"channel_name"`
		ReplyCount  int    `json:"reply_count,omitempty"`
	}
	if err := json.Unmarshal([]byte(meta), &parsed); err != nil {
		return "{}"
	}
	out, _ := json.Marshal(parsed)
	return string(out)
}
