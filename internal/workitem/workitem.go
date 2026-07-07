// Package workitem materializes work items: units of work that link a
// ticket to its commits, PRs, and discussions across sources. Linking is
// deterministic — it reads the issue keys and commit SHAs that collectors
// already store in activity metadata; no LLM is involved.
package workitem

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/pavelpilyak/devrecall/internal/storage"
	"github.com/pavelpilyak/devrecall/pkg/models"
)

// Link kinds recorded on activity_work_items rows.
const (
	LinkIssueKey = "issue_key" // activity references the ticket key
	LinkPRSHA    = "pr_sha"    // commit reached the item through a PR's commit list
	LinkSelf     = "self"      // the activity IS the ticket/PR the item represents
)

// Work item kinds.
const (
	KindTicket = "ticket"
	KindPR     = "pr"
)

// Stats reports what a Materialize run produced.
type Stats struct {
	WorkItems int
	Links     int
}

// ExtractIssueKeys pulls a unified list of ticket keys from an activity's
// metadata, accepting both the singular `issue_key` (Jira) and plural
// `issue_keys` (everything else) shapes. Keys are uppercased and deduped,
// singular first.
func ExtractIssueKeys(metadata string) []string {
	if metadata == "" {
		return nil
	}
	var parsed struct {
		IssueKey  string   `json:"issue_key"`
		IssueKeys []string `json:"issue_keys"`
	}
	if err := json.Unmarshal([]byte(metadata), &parsed); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	if parsed.IssueKey != "" {
		k := strings.ToUpper(parsed.IssueKey)
		seen[k] = true
		out = append(out, k)
	}
	for _, k := range parsed.IssueKeys {
		k = strings.ToUpper(k)
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// activityMeta is the union of the metadata fields the linker reads,
// across all collector-specific metadata shapes.
type activityMeta struct {
	IssueSummary string   `json:"issue_summary"` // jira ticket title
	IssueTitle   string   `json:"issue_title"`   // linear issue title
	Status       string   `json:"status"`
	ToStatus     string   `json:"to_status"` // set on status-transition activities
	URL          string   `json:"url"`
	CommitSHAs   []string `json:"commit_shas"` // PRs/MRs
}

// Title source ranks: a ticket's own summary beats a PR title beats a
// commit subject. Within the ticket/PR rank the latest activity wins;
// for commits the first one wins (the subject that started the work).
const (
	rankCommit = 1
	rankPR     = 2
	rankTicket = 3
)

type itemState struct {
	kind            string
	title           string
	titleRank       int
	status          string
	statusChangedAt time.Time
	url             string
	urlRank         int
	first, last     time.Time
}

// Materialize recomputes the work_items and activity_work_items tables
// from scratch out of current activity metadata. It is deterministic and
// idempotent: running it twice yields identical state, and a re-run after
// new activities arrive (e.g. the Jira ticket for already-linked commits)
// merges them into the existing items by key.
func Materialize(db *storage.DB) (Stats, error) {
	activities, err := db.ListActivityHeaders()
	if err != nil {
		return Stats{}, err
	}

	// Ascending order makes "latest wins" resolution a simple overwrite.
	sort.Slice(activities, func(i, j int) bool {
		return activities[i].Timestamp.Before(activities[j].Timestamp)
	})

	// Index commits by the SHA suffix of their source_id ("<repo>:<sha>")
	// so PR commit lists resolve without per-PR table scans.
	type commitRef struct {
		id int64
		ts time.Time
	}
	shaIndex := make(map[string]commitRef)
	for _, a := range activities {
		if a.Type != models.TypeCommit {
			continue
		}
		if i := strings.LastIndex(a.SourceID, ":"); i >= 0 && i < len(a.SourceID)-1 {
			shaIndex[a.SourceID[i+1:]] = commitRef{id: a.ID, ts: a.Timestamp}
		}
	}

	items := make(map[string]*itemState)
	touch := func(key string, ts time.Time) *itemState {
		st, ok := items[key]
		if !ok {
			st = &itemState{kind: KindTicket, first: ts, last: ts}
			items[key] = st
			return st
		}
		if ts.Before(st.first) {
			st.first = ts
		}
		if ts.After(st.last) {
			st.last = ts
		}
		return st
	}
	setTitle := func(st *itemState, title string, rank int) {
		if title == "" {
			return
		}
		if rank > st.titleRank || (rank == st.titleRank && rank != rankCommit) {
			st.title = title
			st.titleRank = rank
		}
	}
	setURL := func(st *itemState, url string, rank int) {
		if url != "" && rank >= st.urlRank {
			st.url = url
			st.urlRank = rank
		}
	}

	linkSeen := make(map[int64]map[string]bool)
	var links []storage.WorkItemLink
	addLink := func(activityID int64, key, kind string, ts time.Time) {
		if linkSeen[activityID][key] {
			return
		}
		if linkSeen[activityID] == nil {
			linkSeen[activityID] = make(map[string]bool)
		}
		linkSeen[activityID][key] = true
		links = append(links, storage.WorkItemLink{ActivityID: activityID, Key: key, LinkKind: kind})
		touch(key, ts)
	}

	for _, a := range activities {
		var meta activityMeta
		if a.Metadata != "" {
			json.Unmarshal([]byte(a.Metadata), &meta)
		}
		keys := ExtractIssueKeys(a.Metadata)
		isTicket := a.Type == models.TypeTicket || a.Type == models.TypeIssue
		isPR := a.Type == models.TypePullRequest || a.Type == models.TypeMergeRequest

		for i, key := range keys {
			kind := LinkIssueKey
			if isTicket && i == 0 {
				kind = LinkSelf
			}
			addLink(a.ID, key, kind, a.Timestamp)

			// Only the activity's primary key gets its title/status/url —
			// a ticket that merely mentions PROJ-456 doesn't describe it.
			if i != 0 {
				continue
			}
			st := items[key]
			switch {
			case isTicket:
				title := meta.IssueSummary
				if title == "" {
					title = meta.IssueTitle
				}
				// Status transitions carry titles like "PROJ-1 moved to
				// Done" — never let those name the work item.
				if title == "" && meta.ToStatus == "" {
					title = a.Title
				}
				setTitle(st, title, rankTicket)
				setURL(st, meta.URL, rankTicket)
				if meta.ToStatus != "" {
					st.status = meta.ToStatus
					st.statusChangedAt = a.Timestamp
				} else if meta.Status != "" {
					st.status = meta.Status
				}
			case isPR:
				setTitle(st, a.Title, rankPR)
				setURL(st, meta.URL, rankPR)
			case a.Type == models.TypeCommit:
				setTitle(st, a.Title, rankCommit)
			}
		}

		if !isPR {
			continue
		}

		// PR→commit propagation: the PR's commits belong to the same work
		// item(s). A PR with no ticket reference becomes its own item.
		targets := keys
		if len(targets) == 0 {
			key := "pr:" + string(a.Source) + ":" + a.SourceID
			st := touch(key, a.Timestamp)
			st.kind = KindPR
			setTitle(st, a.Title, rankPR)
			setURL(st, meta.URL, rankPR)
			addLink(a.ID, key, LinkSelf, a.Timestamp)
			targets = []string{key}
		}
		for _, sha := range meta.CommitSHAs {
			ref, ok := shaIndex[sha]
			if !ok {
				continue
			}
			for _, key := range targets {
				addLink(ref.id, key, LinkPRSHA, ref.ts)
			}
		}
	}

	desired := make([]models.WorkItem, 0, len(items))
	for key, st := range items {
		desired = append(desired, models.WorkItem{
			Key:             key,
			Kind:            st.kind,
			Title:           st.title,
			Status:          st.status,
			StatusChangedAt: st.statusChangedAt,
			URL:             st.url,
			FirstSeen:       st.first,
			LastSeen:        st.last,
		})
	}
	sort.Slice(desired, func(i, j int) bool { return desired[i].Key < desired[j].Key })

	if err := db.ReplaceWorkItems(desired, links); err != nil {
		return Stats{}, err
	}
	return Stats{WorkItems: len(desired), Links: len(links)}, nil
}
