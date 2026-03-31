package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/pavelpiliak/devrecall/pkg/models"
)

const (
	defaultBaseURL = "https://slack.com/api"
	pageSize       = 100
)

// messageMeta is stored as JSON in Activity.Metadata.
type messageMeta struct {
	ChannelID     string   `json:"channel_id"`
	ChannelName   string   `json:"channel_name"`
	ThreadTS      string   `json:"thread_ts,omitempty"`
	IsThreadReply bool     `json:"is_thread_reply,omitempty"`
	ReplyCount    int      `json:"reply_count,omitempty"`
	Permalink     string   `json:"permalink,omitempty"`
	Participants  []string `json:"participants,omitempty"`
}

// Collector gathers message activity from Slack.
type Collector struct {
	token   string
	baseURL string
	client  *http.Client
}

// New creates a Slack collector with the given user OAuth token.
func New(token string) *Collector {
	return &Collector{
		token:   token,
		baseURL: defaultBaseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWithClient creates a collector with a custom HTTP client and base URL (for testing).
func NewWithClient(token, baseURL string, client *http.Client) *Collector {
	return &Collector{
		token:   token,
		baseURL: baseURL,
		client:  client,
	}
}

func (c *Collector) Name() models.Source {
	return models.SourceSlack
}

// Collect fetches the user's Slack messages since the given time.
// If since is zero, it defaults to 24 hours ago.
func (c *Collector) Collect(ctx context.Context) ([]models.Activity, error) {
	return c.CollectSince(ctx, time.Now().Add(-24*time.Hour))
}

// CollectSince fetches messages from the given time onward.
func (c *Collector) CollectSince(ctx context.Context, since time.Time) ([]models.Activity, error) {
	messages, err := c.searchMessages(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("searching messages: %w", err)
	}

	var activities []models.Activity
	seen := make(map[string]bool)

	for _, msg := range messages {
		sourceID := fmt.Sprintf("slack:%s:%s", msg.Channel.ID, msg.TS)
		if seen[sourceID] {
			continue
		}
		seen[sourceID] = true

		meta := messageMeta{
			ChannelID:   msg.Channel.ID,
			ChannelName: msg.Channel.Name,
			Permalink:   msg.Permalink,
		}

		title := fmt.Sprintf("Message in #%s", msg.Channel.Name)
		content := msg.Text

		// If this message is part of a thread, fetch thread context.
		if msg.ThreadTS != "" && msg.ThreadTS != msg.TS {
			meta.IsThreadReply = true
			meta.ThreadTS = msg.ThreadTS
			title = fmt.Sprintf("Thread reply in #%s", msg.Channel.Name)
		} else if msg.ThreadTS == msg.TS && msg.ReplyCount > 0 {
			// This is a thread parent with replies — fetch the thread.
			meta.ThreadTS = msg.ThreadTS
			meta.ReplyCount = msg.ReplyCount

			thread, err := c.fetchThread(ctx, msg.Channel.ID, msg.ThreadTS)
			if err == nil && len(thread) > 0 {
				meta.Participants = threadParticipants(thread)
				meta.ReplyCount = len(thread) - 1 // exclude parent
				title = fmt.Sprintf("Thread in #%s (%d replies)", msg.Channel.Name, meta.ReplyCount)
			}
		}

		metaJSON, _ := json.Marshal(meta)

		activities = append(activities, models.Activity{
			Source:    models.SourceSlack,
			SourceID:  sourceID,
			Type:      models.TypeMessage,
			Title:     title,
			Content:   content,
			Metadata:  string(metaJSON),
			Timestamp: tsToTime(msg.TS),
		})
	}

	return activities, nil
}

// searchMessages uses Slack's search.messages API to find the user's own messages.
func (c *Collector) searchMessages(ctx context.Context, since time.Time) ([]searchMatch, error) {
	query := fmt.Sprintf("from:me after:%s", since.Format("2006-01-02"))
	var allMatches []searchMatch
	page := 1

	for {
		params := url.Values{
			"query": {query},
			"sort":  {"timestamp"},
			"count": {strconv.Itoa(pageSize)},
			"page":  {strconv.Itoa(page)},
		}

		var resp searchResponse
		if err := c.apiGet(ctx, "/search.messages", params, &resp); err != nil {
			return nil, err
		}
		if !resp.OK {
			return nil, fmt.Errorf("slack API error: %s", resp.Error)
		}

		allMatches = append(allMatches, resp.Messages.Matches...)

		if len(allMatches) >= resp.Messages.Total || len(resp.Messages.Matches) < pageSize {
			break
		}
		page++
	}

	return allMatches, nil
}

// fetchThread retrieves all replies in a thread.
func (c *Collector) fetchThread(ctx context.Context, channelID, threadTS string) ([]threadMessage, error) {
	params := url.Values{
		"channel": {channelID},
		"ts":      {threadTS},
		"limit":   {"200"},
	}

	var resp threadResponse
	if err := c.apiGet(ctx, "/conversations.replies", params, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("slack API error: %s", resp.Error)
	}

	return resp.Messages, nil
}

// apiGet makes an authenticated GET request to the Slack API.
func (c *Collector) apiGet(ctx context.Context, path string, params url.Values, dst any) error {
	reqURL := c.baseURL + path + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack request %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := resp.Header.Get("Retry-After")
		return fmt.Errorf("rate limited (retry after %s seconds)", retryAfter)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("slack %s returned %d: %s", path, resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(dst)
}

// tsToTime converts a Slack timestamp ("1679900000.123456") to time.Time.
func tsToTime(ts string) time.Time {
	// Slack timestamps are Unix seconds with microsecond decimal.
	dotIdx := -1
	for i, ch := range ts {
		if ch == '.' {
			dotIdx = i
			break
		}
	}

	var secs int64
	if dotIdx > 0 {
		secs, _ = strconv.ParseInt(ts[:dotIdx], 10, 64)
	} else {
		secs, _ = strconv.ParseInt(ts, 10, 64)
	}
	return time.Unix(secs, 0).UTC()
}

// threadParticipants extracts unique user IDs from a thread (excluding bots).
func threadParticipants(msgs []threadMessage) []string {
	seen := make(map[string]bool)
	var users []string
	for _, m := range msgs {
		if m.User != "" && !seen[m.User] {
			seen[m.User] = true
			users = append(users, m.User)
		}
	}
	return users
}

// UserProfile holds the authenticated Slack user's profile info.
type UserProfile struct {
	UserID string
	Email  string
	Name   string
}

// GetUserProfile fetches the authenticated user's profile, including email.
// Requires the users:read and users:read.email scopes.
func (c *Collector) GetUserProfile(ctx context.Context, userID string) (*UserProfile, error) {
	params := url.Values{"user": {userID}}

	var resp userInfoResponse
	if err := c.apiGet(ctx, "/users.info", params, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("slack API error: %s", resp.Error)
	}

	return &UserProfile{
		UserID: resp.User.ID,
		Email:  resp.User.Profile.Email,
		Name:   resp.User.Profile.RealName,
	}, nil
}

// Slack API response types.

type userInfoResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	User  struct {
		ID      string `json:"id"`
		Profile struct {
			Email    string `json:"email"`
			RealName string `json:"real_name"`
		} `json:"profile"`
	} `json:"user"`
}

type searchResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Messages struct {
		Total   int           `json:"total"`
		Matches []searchMatch `json:"matches"`
	} `json:"messages"`
}

type searchMatch struct {
	TS         string `json:"ts"`
	Text       string `json:"text"`
	Permalink  string `json:"permalink"`
	ThreadTS   string `json:"thread_ts,omitempty"`
	ReplyCount int    `json:"reply_count,omitempty"`
	Channel    struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"channel"`
}

type threadResponse struct {
	OK       bool            `json:"ok"`
	Error    string          `json:"error,omitempty"`
	Messages []threadMessage `json:"messages"`
}

type threadMessage struct {
	User string `json:"user"`
	Text string `json:"text"`
	TS   string `json:"ts"`
}
