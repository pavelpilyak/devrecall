package models

import "time"

// Source represents where an activity came from.
type Source string

const (
	SourceGit      Source = "git"
	SourceSlack    Source = "slack"
	SourceCalendar Source = "calendar"
	SourceGitHub   Source = "github"
	SourceJira     Source = "jira"
	SourceLinear   Source = "linear"
)

// ActivityType categorizes what kind of work event this is.
type ActivityType string

const (
	TypeCommit       ActivityType = "commit"
	TypeMessage      ActivityType = "message"
	TypeMeeting      ActivityType = "meeting"
	TypeTicket       ActivityType = "ticket"
	TypeReview       ActivityType = "review"
	TypePullRequest  ActivityType = "pull_request"
	TypeIssue        ActivityType = "issue"
)

// Activity is a single work event collected from any source.
type Activity struct {
	ID         int64        `json:"id"`
	Source     Source       `json:"source"`
	SourceID   string       `json:"source_id"`
	IdentityID int64        `json:"identity_id,omitempty"`
	Type       ActivityType `json:"type"`
	Title      string       `json:"title"`
	Content    string       `json:"content,omitempty"`
	Metadata   string       `json:"metadata,omitempty"`
	Timestamp  time.Time    `json:"timestamp"`
}

// Identity represents a person across different services.
type Identity struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	IsSelf bool   `json:"is_self"`
}

// Summary is an AI-generated aggregation of activities over a period.
type Summary struct {
	ID            int64  `json:"id"`
	PeriodType    string `json:"period_type"` // "daily", "weekly", "monthly", "quarterly"
	PeriodStart   string `json:"period_start"`
	PeriodEnd     string `json:"period_end"`
	SummaryText   string `json:"summary_text"`
	Highlights    string `json:"highlights,omitempty"` // JSON: achievements, collaborators, metrics
	ActivityCount int    `json:"activity_count"`
}
